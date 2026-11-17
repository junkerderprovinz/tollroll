//go:build !linux

package host

import "errors"

func statfs(string) (Space, error) { return Space{}, errors.ErrUnsupported }
