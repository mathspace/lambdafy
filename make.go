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
	"log"
	"time"

	dockertypes "github.com/docker/docker/api/types"
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

	// Check if the image is already lambdafied with the same proxy version.
	// If so, we can skip the rest of the process.

	proxyChksum := sha256.Sum256(proxyBinary)
	proxyChksumHex := hex.EncodeToString(proxyChksum[:])
	if proxyChksumHex == img.Config.Labels["lambdafy.proxy.checksum"] {
		log.Print("image is already lambdafied with the same proxy version - skipping")
		return nil
	}

	if img.Architecture != "amd64" || img.Os != "linux" {
		return fmt.Errorf("platform of docker image '%s' must be linux/amd64", imgName)
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
FROM --platform=linux/amd64 %s
RUN rm -f /lambdafy-proxy
COPY --chmod=775 lambdafy-proxy /
ENTRYPOINT %s
CMD %s
LABEL "lambdafy.proxy.checksum"="%s"
`, imgName, string(ep), string(cmd), proxyChksumHex)

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

	return nil
}
