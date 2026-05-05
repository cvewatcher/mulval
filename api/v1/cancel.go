package apiv1

const (
	CancelStream  = "MULVAL_CANCEL"
	CancelSubject = "mulval.cancel"

	TypeCancel = "dev.cvewatcher.mulval.operation.cancel"
)

// Cancel is the CloudEvent data payload for a cancel request.
type Cancel struct {
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}
