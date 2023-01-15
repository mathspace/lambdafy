package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	dockerjsonmsg "github.com/docker/docker/pkg/jsonmessage"
)

// canonicalizePolicyString canonicalizes a policy string by unmarshaling and
// marshaling it. This is used to ensure that the policy string is in a
// format that results in consistent hashing.
func canonicalizePolicyString(s string, urlenc bool) (string, error) {
	var err error
	if urlenc {
		s, err = url.QueryUnescape(s)
		if err != nil {
			return "", err
		}
	}
	var p interface{}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// retryOnResourceConflict retries a function if it returns a resource conflict error.
// It also retries on a few other errors that are known to be transient.
func retryOnResourceConflict(ctx context.Context, fn func() error) error {
	for {
		err := fn()
		switch {
		case err == nil:
			return err
		case strings.Contains(err.Error(), "ARN does not refer to a valid principal"):
		case strings.Contains(err.Error(), "role defined for the function cannot be assumed"):
		case strings.Contains(err.Error(), "ResourceConflictException"):
			if strings.Contains(err.Error(), "exists") {
				return err
			}
		default:
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
