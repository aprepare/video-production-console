//go:build !windows

package main

func defaultOpenUI(string) error {
	return nil
}
