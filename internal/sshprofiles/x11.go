package sshprofiles

import (
	"regexp"
	"strconv"
)

const maxX11Display = 59535
const maxX11Screen = 65535

var localX11Display = regexp.MustCompile(`^(?:(?:unix|localhost|127\.0\.0\.1|\[::1\]))?:(\d+)(?:\.(\d+))?$`)

type x11Options struct {
	Display string `json:"display,omitempty"`
}

func (options x11Options) valid() bool {
	if options.Display == "" {
		return true
	}
	if len(options.Display) > 256 {
		return false
	}
	parts := localX11Display.FindStringSubmatch(options.Display)
	if parts == nil {
		return false
	}
	display, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil || display > maxX11Display {
		return false
	}
	if parts[2] == "" {
		return true
	}
	screen, err := strconv.ParseUint(parts[2], 10, 16)
	return err == nil && screen <= maxX11Screen
}
