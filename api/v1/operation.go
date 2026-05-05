package apiv1

const (
	OperationStream      = "MULVAL_OPERATION"
	OperationSubjectBase = "mulval.operation.update"

	TypeOperationUpdate = "dev.cvewatcher.mulval.operation"
)

type OperationUpdate struct {
	ID string `json:"id"`
}
