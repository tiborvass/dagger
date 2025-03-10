package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dagger/dagger/internal/dagger"
)

func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yymmddhhmmss"
	return t.Format("060102150405")
}

func getVersionTag(ctx context.Context, source *dagger.Directory) (string, string, error) {
	next := "v0.16.3"
	digest, err := source.Digest(ctx)
	if err != nil {
		return "", "", err
	}
	if _, newDigest, ok := strings.Cut(digest, ":"); ok {
		digest = newDigest
	}
	// NOTE: the timestamp is empty here to prevent unnecessary rebuilds
	version := fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Time{}), digest[:12])
	tag := "1ab60f070617b193bd14dcb6c247a31391ccbdb9"
	return version, tag, nil
}

func New(
	ctx context.Context,

	// +optional
	runnerHost string,

	// +optional
	// +defaultPath="/"
	// +ignore=["*", ".*", "!/cmd/dagger/*", "!**/go.sum", "!**/go.mod", "!**/*.go", "!**.graphql"]
	source *dagger.Directory,
	// Base image for go build environment
	// +optional
	base *dagger.Container,
) (*DaggerCli, error) {
	// FIXME: this go builder config is duplicated with engine build
	// move into a shared engine/builder module
	version, imageTag, err := getVersionTag(ctx, source)
	if err != nil {
		return nil, err
	}
	values := []string{
		// FIXME: how to avoid duplication with engine module?
		"github.com/dagger/dagger/engine.Version=" + version,
		"github.com/dagger/dagger/engine.Tag=" + imageTag,
	}
	if runnerHost != "" {
		values = append(values, "main.RunnerHost="+runnerHost)
	}
	return &DaggerCli{
		Gomod: dag.Go(source, dagger.GoOpts{
			Base:   base,
			Values: values,
		}),
	}, nil
}

type DaggerCli struct {
	Gomod *dagger.Go // +private
}

// Build the dagger CLI binary for a single platform
func (cli DaggerCli) Binary(
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return cli.Gomod.Binary("./cmd/dagger", dagger.GoBinaryOpts{
		Platform:  platform,
		NoSymbols: true,
		NoDwarf:   true,
	})
}

// Generate a markdown CLI reference doc
func (cli DaggerCli) Reference(
	// +optional
	frontmatter string,
	// +optional
	// Include experimental commands
	includeExperimental bool,
) *dagger.File {
	cmd := []string{"go", "run", "./cmd/dagger", "gen", "--output", "cli.mdx"}
	if includeExperimental {
		cmd = append(cmd, "--include-experimental")
	}
	if frontmatter != "" {
		cmd = append(cmd, "--frontmatter="+frontmatter)
	}
	return cli.Gomod.
		Env().
		WithExec(cmd).
		File("cli.mdx")
}
