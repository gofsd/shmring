//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runEnv runs name with args, overlaying extraEnv on top of the current
// environment, streaming output to this process's stdout/stderr.
func runEnv(extraEnv map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	fmt.Println("+", envPrefix(extraEnv)+strings.Join(append([]string{name}, args...), " "))
	return cmd.Run()
}

func envPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range env {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte(' ')
	}
	return b.String()
}

// lookPath reports whether name is found on PATH.
func lookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
