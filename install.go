package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultReplaceID      = "AABBCCDD"
	defaultBootstrapDir   = "bootstrap"
	defaultRepo           = "https://github.com/khulnasoft/superkit.git"
	defaultCloneTimeout   = 120 * time.Second
	secretByteLen         = 32
	binaryNullByte uint8  = 0
)

func main() {
	// Flags
	repo := flag.String("repo", defaultRepo, "Repository to clone")
	branch := flag.String("branch", "", "Branch to checkout (optional)")
	force := flag.Bool("force", false, "Force overwrite if project folder already exists")
	id := flag.String("id", defaultReplaceID, "Identifier to replace inside files")
	bootstrap := flag.String("bootstrap", defaultBootstrapDir, "Name of bootstrap folder inside the repo")
	timeout := flag.Duration("timeout", defaultCloneTimeout, "Timeout for git clone operation")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options] project-name\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	projectName := args[0]

	log.SetFlags(0)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// create temp dir to clone into
	tmpDir, err := os.MkdirTemp("", "superkit-clone-*")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}
	// ensure cleanup
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	log.Printf("-- cloning %s into %s", *repo, tmpDir)
	if err := cloneRepo(ctx, *repo, tmpDir, *branch); err != nil {
		log.Fatalf("git clone failed: %v", err)
	}

	srcBootstrap := filepath.Join(tmpDir, *bootstrap)
	if _, err := os.Stat(srcBootstrap); os.IsNotExist(err) {
		log.Fatalf("bootstrap folder %q not found in cloned repo", srcBootstrap)
	}

	// Destination path is relative to current working dir
	dest := filepath.Join(".", projectName)

	// Check existing destination
	if _, err := os.Stat(dest); err == nil {
		if *force {
			log.Printf("-- removing existing project folder %s (force)", dest)
			if err := os.RemoveAll(dest); err != nil {
				log.Fatalf("failed to remove existing project folder: %v", err)
			}
		} else {
			log.Fatalf("destination %s already exists; rerun with -force to remove it", dest)
		}
	}

	// Try rename first (fast), fallback to copy if cross-device
	log.Printf("-- moving %s -> %s", srcBootstrap, dest)
	if err := os.Rename(srcBootstrap, dest); err != nil {
		log.Printf("rename failed (might be cross-device); falling back to copy: %v", err)
		if err := copyDir(srcBootstrap, dest); err != nil {
			log.Fatalf("failed to copy bootstrap folder: %v", err)
		}
	}

	// Replace identifiers in text files
	log.Printf("-- replacing identifier %q with project name %q", *id, projectName)
	if err := replaceIdentifierInTree(dest, *id, projectName); err != nil {
		log.Fatalf("failed to replace identifiers: %v", err)
	}

	// Handle .env
	envLocal := filepath.Join(dest, ".env.local")
	envFile := filepath.Join(dest, ".env")
	envExample := filepath.Join(dest, ".env.example")
	if err := ensureEnv(envLocal, envExample, envFile); err != nil {
		log.Fatalf("env handling failed: %v", err)
	}
	// Generate secret and inject
	secret := generateSecret()
	if err := injectSecret(envFile, secret); err != nil {
		log.Fatalf("failed to inject secret: %v", err)
	}

	log.Printf("-- project (%s) successfully installed!", projectName)
}

// cloneRepo clones repo into dest. If branch is non-empty, tries to checkout that branch.
// It performs a shallow clone to speed things up.
func cloneRepo(ctx context.Context, repo, dest, branch string) error {
	args := []string{"clone", "--depth", "1", repo, dest}
	if branch != "" {
		// If branch provided, use --branch so clone will get that branch shallowly
		args = []string{"clone", "--depth", "1", "--branch", branch, repo, dest}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone error: %w; output: %s", err, out.String())
	}
	return nil
}

// copyDir recursively copies a directory from src to dst preserving file modes.
func copyDir(src, dst string) error {
	// Ensure destination parent exists
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		// file
		return copyFileWithMode(p, target, info.Mode())
	})
}

func copyFileWithMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// replaceIdentifierInTree walks the directory tree rooted at root and replaces occurrences
// of oldID with newVal in text files. It skips common binary files and .git.
func replaceIdentifierInTree(root, oldID, newVal string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// skip .git
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		// Read file bytes
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// heuristics: skip binary files (contains null byte)
		if isBinary(b) {
			return nil
		}
		if !bytes.Contains(b, []byte(oldID)) {
			return nil
		}
		newContent := bytes.ReplaceAll(b, []byte(oldID), []byte(newVal))
		// preserve file mode
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, newContent, info.Mode()); err != nil {
			return err
		}
		return nil
	})
}

func isBinary(b []byte) bool {
	// simple check: presence of a null byte
	return bytes.IndexByte(b, binaryNullByte) != -1
}

// ensureEnv ensures there's a .env file at dest. Prefer renaming .env.local, else copy .env.example, else create minimal file.
func ensureEnv(envLocal, envExample, dest string) error {
	// If .env already exists, do nothing
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	// try .env.local
	if _, err := os.Stat(envLocal); err == nil {
		return os.Rename(envLocal, dest)
	}
	// try .env.example
	if _, err := os.Stat(envExample); err == nil {
		in, err := os.ReadFile(envExample)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, in, 0o600)
	}
	// create minimal .env
	content := "APP_SECRET={{app_secret}}\n"
	return os.WriteFile(dest, []byte(content), 0o600)
}

// injectSecret replaces "{{app_secret}}" inside envPath with the provided secret.
// If placeholder not present, it will append APP_SECRET to the file.
func injectSecret(envPath, secret string) error {
	b, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	content := string(b)
	if strings.Contains(content, "{{app_secret}}") {
		content = strings.ReplaceAll(content, "{{app_secret}}", secret)
	} else if strings.Contains(content, "APP_SECRET=") {
		// replace existing APP_SECRET value
		lines := strings.Split(content, "\n")
		for i, ln := range lines {
			if strings.HasPrefix(ln, "APP_SECRET=") {
				lines[i] = "APP_SECRET=" + secret
			}
		}
		content = strings.Join(lines, "\n")
	} else {
		// append
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "APP_SECRET=" + secret + "\n"
	}
	// preserve original mode if possible
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(envPath); err == nil {
		mode = info.Mode()
	}
	return os.WriteFile(envPath, []byte(content), mode)
}

func generateSecret() string {
	b := make([]byte, secretByteLen)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate secret: %v", err)
	}
	return hex.EncodeToString(b)
}
