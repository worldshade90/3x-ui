//go:build linux

package speedlimit

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	applyMu sync.Mutex
	// idle means the last apply removed our rules, so an empty plan can skip exec.
	// The first call is not idle: a previous process may have left the table behind.
	idle bool
)

const ruleMark = "0x3a000000/0xff000000"

// Apply installs nftables meters for clients, or removes them when clients is empty.
// It only owns table inet xui_speed and fwmark pref 50; it does not replace a qdisc.
func Apply(ctx context.Context, clients []Client) error {
	if ctx == nil {
		ctx = context.Background()
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	script := RenderRules(clients)
	if script == "" {
		if idle {
			return nil
		}
		_ = run(ctx, "nft", "delete", "table", "inet", tableName)
		deleteRule(ctx, "")
		deleteRule(ctx, "-6")
		idle = true
		return nil
	}
	idle = false
	if err := ensureRule(ctx, ""); err != nil {
		return err
	}
	if err := ensureRule(ctx, "-6"); err != nil {
		return err
	}
	return runStdin(ctx, script, "nft", "-f", "-")
}

func ensureRule(ctx context.Context, familyFlag string) error {
	args := []string{}
	if familyFlag != "" {
		args = append(args, familyFlag)
	}
	out, err := output(ctx, "ip", append(args, "rule", "show")...)
	if err != nil {
		return err
	}
	if strings.Contains(out, ruleMark) {
		return nil
	}
	add := append(args, "rule", "add", "pref", "50", "fwmark", ruleMark, "lookup", "main")
	if err := run(ctx, "ip", add...); err != nil && !strings.Contains(err.Error(), "File exists") {
		return err
	}
	return nil
}

func deleteRule(ctx context.Context, familyFlag string) {
	args := []string{}
	if familyFlag != "" {
		args = append(args, familyFlag)
	}
	args = append(args, "rule", "del", "pref", "50", "fwmark", ruleMark, "lookup", "main")
	_ = run(ctx, "ip", args...)
}

func run(ctx context.Context, name string, args ...string) error {
	return runStdin(ctx, "", name, args...)
}

func runStdin(ctx context.Context, stdin, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}
