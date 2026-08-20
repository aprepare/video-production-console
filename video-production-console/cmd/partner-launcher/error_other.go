//go:build !windows

package main

import (
	"fmt"
	"os"
)

func reportLaunchError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
}
