package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

func cmdErr(err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
	}
	return err
}

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
