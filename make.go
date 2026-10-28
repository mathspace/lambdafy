package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockerimagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

//go:generate ./build-proxy.sh

//go:embed proxy-linux-amd64
var proxyBinary []byte

var makeCmd = &cobra.Command{
	Use:   "make image-name",
	Short: "Modify a docker image by adding lambdafy proxy to it",
	Args:  cobra.ExactArgs(1),
	RunE: func(c *cobra.Command, args []string) error {
		return lambdafyImage(args[0])
	},
}

// lambdafyImage modifies the image by adding lambda proxy to it.
func lambdafyImage(imgName string) error {

	ctx := context.Background()

	// Setup client

	dc, err := dockerclient.NewClientWithOpts(
		dockerclient.WithAPIVersionNegotiation(),
		dockerclient.FromEnv,
	)
	if err != nil {
		return fmt.Errorf("failed to get docker client: %s", err)
	}

	// Extract entrypoint from the given imgName as it needs to be prefixed with
	// the proxy command

	img, _, err := dc.ImageInspectWithRaw(ctx, imgName)
	if err != nil {
		return fmt.Errorf("failed to inspect docker image '%s': %s", imgName, err)
	}

	// Always build so repository metadata can invalidate stale certificate layers.

	proxyChksum := sha256.Sum256(proxyBinary)
	proxyChksumHex := hex.EncodeToString(proxyChksum[:])

	if img.Architecture != "amd64" || img.Os != "linux" {
		return fmt.Errorf("platform of docker image '%s' must be linux/amd64", imgName)
	}

	// Keep one local source image so repeated refreshes do not layer onto their output.
	baseRef := img.Config.Labels["lambdafy.source.image"]
	if baseRef != "" {
		if !strings.HasPrefix(baseRef, "lambdafy-base:") {
			return fmt.Errorf("invalid lambdafy source image: %s", baseRef)
		}
		previousRef := strings.Replace(baseRef, "lambdafy-base:", "lambdafy-wrapped:", 1)
		previous, _, inspectErr := dc.ImageInspectWithRaw(ctx, previousRef)
		if inspectErr != nil {
			return fmt.Errorf("inspect previous wrapped image; rebuild the application image first: %w", inspectErr)
		}
		if previous.ID == img.ID {
			img, _, err = dc.ImageInspectWithRaw(ctx, baseRef)
			if err != nil {
				return fmt.Errorf("inspect retained source image; rebuild the application image first: %w", err)
			}
			if img.ID != "sha256:"+strings.TrimPrefix(baseRef, "lambdafy-base:") {
				return fmt.Errorf("retained application image changed: %s", baseRef)
			}
		} else {
			baseRef = "" // The caller built a new application from a wrapped parent.
		}
	}
	if baseRef == "" {
		baseRef = "lambdafy-base:" + strings.TrimPrefix(img.ID, "sha256:")
		if err := dc.ImageTag(ctx, img.ID, baseRef); err != nil {
			return fmt.Errorf("retain application image: %w", err)
		}
	}
	wrappedRef := strings.Replace(baseRef, "lambdafy-base:", "lambdafy-wrapped:", 1)
	runtimeUser := img.Config.User
	if runtimeUser == "" {
		runtimeUser = "0"
	}
	pull, err := dc.ImagePull(ctx, "debian:bookworm-slim", dockerimagetypes.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull certificate image: %w", err)
	}
	err = processDockerResponse(pull)
	_ = pull.Close()
	if err != nil {
		return fmt.Errorf("pull certificate image: %w", err)
	}

	// In case the image is already lambdafied, we need to remove the old proxy
	// entry from command line.

	if len(img.Config.Entrypoint) > 0 && img.Config.Entrypoint[0] == "/lambdafy-proxy" {
		img.Config.Entrypoint = img.Config.Entrypoint[1:]
	}

	ep, err := json.Marshal(append([]string{"/lambdafy-proxy"}, img.Config.Entrypoint...))
	if err != nil {
		return fmt.Errorf("failed to marshal docker image '%s' entrypoint to json: %s", imgName, err)
	}

	cmd, err := json.Marshal(img.Config.Cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal docker image '%s' command to json: %s", imgName, err)
	}

	// Build a new docker image with the proxy embedded

	dockerFile := fmt.Sprintf(`
# Certificates
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS certs
# This line busts the cache if there's security updates.
ADD https://deb.debian.org/debian/dists/bookworm/InRelease /tmp/bust-debian
ADD https://deb.debian.org/debian/dists/bookworm-updates/InRelease /tmp/bust-updates
ADD https://security.debian.org/debian-security/dists/bookworm-security/InRelease /tmp/bust-security
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates

FROM --platform=linux/amd64 %s
# This line busts the cache if there's security updates.
ADD https://deb.debian.org/debian/dists/bookworm/InRelease /tmp/bust-debian
ADD https://deb.debian.org/debian/dists/bookworm-updates/InRelease /tmp/bust-updates
ADD https://security.debian.org/debian-security/dists/bookworm-security/InRelease /tmp/bust-security
USER 0
RUN rm -f /lambdafy-proxy
COPY --chmod=775 lambdafy-proxy /
RUN rm -rf /etc/ssl/certs /usr/share/ca-certificates
COPY --from=certs /usr/share/ca-certificates/ /usr/share/ca-certificates/
COPY --from=certs /etc/ssl/certs/ /etc/ssl/certs/
COPY --from=certs /etc/ca-certificates.conf /etc/ca-certificates.conf
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=${SSL_CERT_FILE:-/etc/ssl/certs/ca-certificates.crt} SSL_CERT_DIR=${SSL_CERT_DIR:-/etc/ssl/certs}
USER %s
ENTRYPOINT %s
CMD %s
LABEL "lambdafy.proxy.checksum"="%s" "lambdafy.source.image"="%s"
`, baseRef, runtimeUser, string(ep), string(cmd), proxyChksumHex, baseRef)

	r, w := io.Pipe()

	t := time.UnixMicro(0)
	go func() {
		tr := tar.NewWriter(w)
		_ = tr.WriteHeader(&tar.Header{Name: "Dockerfile", Size: int64(len(dockerFile)), ModTime: t, AccessTime: t, ChangeTime: t})
		_, _ = tr.Write([]byte(dockerFile))
		_ = tr.WriteHeader(&tar.Header{Name: "lambdafy-proxy", Size: int64(len(proxyBinary)), ModTime: t, AccessTime: t, ChangeTime: t})
		_, _ = tr.Write(proxyBinary)
		_ = tr.Close()
		_ = w.Close()
	}()

	resp, err := dc.ImageBuild(ctx, r, dockertypes.ImageBuildOptions{
		Tags:           []string{imgName},
		Version:        dockertypes.BuilderBuildKit,
		Platform:       "linux/amd64",
		SuppressOutput: true,
	})
	if err != nil {
		return fmt.Errorf("failed to build lambdafied image: %s", err)
	}
	defer resp.Body.Close()
	if err := processDockerResponse(resp.Body); err != nil {
		return fmt.Errorf("failed to build lambdafied image: %s", err)
	}
	if err := dc.ImageTag(ctx, imgName, wrappedRef); err != nil {
		return fmt.Errorf("retain wrapped image identity: %w", err)
	}

	return nil
}
