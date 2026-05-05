package operations

import (
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
)

type OperationServer struct {
	longrunningpb.UnimplementedOperationsServer
}

func NewOperationsServer() longrunningpb.OperationsServer {
	return &OperationServer{}
}
