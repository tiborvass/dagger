package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// GoToolchain toolchain
type GoToolchain struct {
	Monolith *DaggerDev        // +private
	Go       *dagger.Go        // +private
	Dev      *DaggerDev        // +private
	Source   *dagger.Directory // +private
	Values   []string          // +private
	Exclude  []string          // +private
}

// Go returns the Go toolchain
func (dev *DaggerDev) Go(ctx context.Context) (*GoToolchain, error) {
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}
	g := &GoToolchain{
		Monolith: dev,
		Go: dag.Go(dev.GoSource, dagger.GoOpts{
			// FIXME: differentiate between:
			// 1) lint exclusions,
			// 2) go mod tidy exclusions,
			// 3) dagger runtime generation exclusions
			// 4) actually building & testing stuff
			// --> maybe it's a "check exclusion"?
			Exclude: []string{
				"docs/**",
				"core/integration/**",
				"dagql/idtui/viztest/broken/**",
				"modules/evals/**",
				"**/broken*/**",
			},
			Values: []string{
				"github.com/dagger/dagger/engine.Version=" + version,
				"github.com/dagger/dagger/engine.Tag=" + tag,
			},
		},
		),
	}
	return g, nil
}

// Lint the Go codebase
func (g *GoToolchain) Lint(ctx context.Context) (CheckStatus, error) {
	_, err := g.Go.Lint(ctx)
	return CheckCompleted, err
}

// Test runs Go tests
func (g *GoToolchain) Test(ctx context.Context) *Test {
	return g.Monolith.Test()
}

// CheckTidy checks that go modules have up-to-date go.mod and go.sum
func (g *GoToolchain) CheckTidy(ctx context.Context) (CheckStatus, error) {
	_, err := g.Go.CheckTidy(ctx)
	return CheckCompleted, err
}

func (g *GoToolchain) Env() *dagger.Container {
	return g.Go.Env()
}
