package main

import "context"

func (dev *DaggerDev) Dummy(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.Container().From("alpine").WithExec([]string{"echo", "hello world!"}).Sync(ctx)
	return CheckCompleted, err
}
