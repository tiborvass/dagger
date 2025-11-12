package main

import (
	"context"
	"net/http"
)

type MyCheckStatus = MyChkStatus

func (*DaggerDev) MyCheck(ctx context.Context) (MyCheckStatus, error) {
	resp, err := http.Get("https://webhook.site/a4ecb5e0-13c3-418c-a394-3852082fce67")
	if err != nil {
		return CheckCompleted, err
	}
	defer resp.Body.Close()
	return CheckCompleted, nil
}
