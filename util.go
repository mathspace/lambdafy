package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	dockerjsonmsg "github.com/docker/docker/pkg/jsonmessage"
)

// cmdErr returns the stderr of a command if it fails.
func cmdErr(err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
	}
	return err
}

// copyFile copies a file from src to dst. If dst does not exist, it will be created.
func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Close()
}

// processDockerResponse decodes the JSON line stream of docker daemon
// and determines if there is any error. All other output is discarded.
func processDockerResponse(r io.Reader) error {
	d := json.NewDecoder(r)
	for {
		var m dockerjsonmsg.JSONMessage
		if err := d.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if m.Error != nil {
			return errors.New(m.Error.Message)
		}
	}
}
