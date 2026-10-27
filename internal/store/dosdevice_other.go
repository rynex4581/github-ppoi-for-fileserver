//go:build !windows

package store

import "errors"

var errDOSDeviceUnsupported = errors.New("DOS デバイス名は Windows でのみ使用可能")

func sysDefineDOSDevice(name, target string) error { return errDOSDeviceUnsupported }

func sysRemoveDOSDevice(name string) error { return errDOSDeviceUnsupported }

func sysQueryDOSDevice(name string) ([]string, error) { return nil, errDOSDeviceUnsupported }
