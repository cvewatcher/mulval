package analysis

import (
	analysispb "github.com/cvewatcher/mulval/proto/api/v1/analysis"
)

type Analyzer struct {
	analysispb.UnimplementedAnalysisServiceServer
}

func NewAnalyzer() analysispb.AnalysisServiceServer {
	return &Analyzer{}
}
