package main

import (
	"archive/tar"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
)

//go:generate sh -c "cd proxy ; CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../proxy-linux-amd64"

//go:embed proxy-linux-amd64
var proxyBinary []byte

var errAlreadyLambdafied = errors.New("docker image already lambdafied")

// makeImage updates the image and adds the lambdafy proxy to it.
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

	if len(img.Config.Entrypoint) > 0 && img.Config.Entrypoint[0] == "/lambdafy-proxy" {
		return errAlreadyLambdafied
	}

	if img.Architecture != "amd64" || img.Os != "linux" {
		return fmt.Errorf("platform of docker image '%s' must be linux/amd64", imgName)
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
COPY --chmod=775 lambdafy-proxy /
ENTRYPOINT %s
CMD %s
`, imgName, string(ep), string(cmd))

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
	if err := processDockerResponse(resp.Body); err != nil {
		resp.Body.Close()
		return fmt.Errorf("failed to build lambdafied image: %s", err)
	}
	resp.Body.Close()

	return nil
}
