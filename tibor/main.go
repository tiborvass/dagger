package main

import (
	"dagger/tibor/internal/dagger"
)

type Tibor struct{}

func (m *Tibor) Repro() *dagger.CheckGroup {
	return dag.ModuleSource("github.com/shykes/dagger/cmd/engine@6eb97d30a2ae6b07d16d295b9bfff965f7cc1a7e").AsModule().Checks()
}
