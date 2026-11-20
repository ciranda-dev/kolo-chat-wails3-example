//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// newNotchPanel returns nil: only macOS has a notch window, and the app runs without one.
func newNotchPanel(*application.App, *NotchService) notchPanel { return nil }
