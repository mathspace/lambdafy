package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	dockerjsonmsg "github.com/docker/docker/pkg/jsonmessage"
)

func retryOnResourceConflict(ctx context.Context, fn func() error) error {
	for {
		err := fn()
		if err == nil || strings.Contains(err.Error(), "role defined for the function cannot be assumed by Lambda") || strings.Contains(err.Error(), "exists") || !strings.Contains(err.Error(), "ResourceConflictException") {
			return err
		}
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

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

// waitOnFunc waits for a lambda function to be ready.
func waitOnFunc(ctx context.Context, lambdaCl *lambda.Client, fnName string) error {
	for {
		fOut, err := lambdaCl.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: &fnName,
		})
		if err != nil {
			return fmt.Errorf("failed poll function state: %s", err)
		}
		switch s := fOut.Configuration.State; s {
		case lambdatypes.StateActive:
			return nil
		case lambdatypes.StatePending:
			time.Sleep(2 * time.Second)
			continue
		default:
			return fmt.Errorf("invalid state while polling: %s", s)
		}
	}
}
