package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// "CI in CI": check that Dagger can still run its own CI
// Note: this doesn't actually call all CI checks: only a small subset,
// selected for maximum coverage of Dagger features with limited compute expenditure.
// The actual checks being performed is an implementation detail, and should NOT be relied on.
// In other words, don't skip running <foo> just because it happens to be run here!
func (dev *DaggerDev) CiInCi(ctx context.Context) (CheckStatus, error) {
	ctr, err := dev.Playground(ctx, DistroAlpine, false, false)
	if err != nil {
		return CheckCompleted, err
	}
	ctr = ctr.
		With(dev.withDockerCfg).
		WithMountedDirectory(".git/", dev.Git.Head().Tree().Directory(".git/"))

	_, err = ctr.
		WithExec([]string{"dagger", "call", "--docker-cfg=file:$HOME/.docker/config.json", "test-sdks"}, dagger.ContainerWithExecOpts{Expand: true}).
		Sync(ctx)
	return CheckCompleted, err
}
