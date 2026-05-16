//go:build !windows

package main

func detectSystemLanguage() language {
	return ""
}
