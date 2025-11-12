package main

// FIXME: stopgap until core API defines MyChkStatus
type MyChkStatus string

const (
	CheckCompleted MyChkStatus = "COMPLETED"
	CheckSkipped   MyChkStatus = "SKIPPED"
)
