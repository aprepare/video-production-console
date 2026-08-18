//go:build !windows

package settings

func validatePlatformLocalPath(string) error { return nil }
