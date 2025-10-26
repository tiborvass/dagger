package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Creates a complete end-to-end build environment with CLI and engine for interactive testing
func (dev *DaggerDev) Playground(
	ctx context.Context,
	// Set target distro
	// +default="alpine"
	image Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
) (*dagger.Container, error) {
	svc := dag.DaggerEngine().Service("", dagger.DaggerEngineServiceOpts{
		Image:       dagger.DaggerEngineDistro(image),
		GpuSupport:  gpuSupport,
		SharedCache: sharedCache,
	})
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return dag.Container().
			WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
			WithServiceBinding("dagger-engine", svc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint),
		nil
}
