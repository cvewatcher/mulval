package integration_test

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/cvewatcher/mulval/proto/api/v1/analysis"
	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_I_Examples(t *testing.T) {
	pwd, _ := os.Getwd()

	integration.ProgramTest(t, &integration.ProgramTestOptions{
		Quick:       true,
		SkipRefresh: true,
		StackName:   stackName(t.Name()),
		Dir:         path.Join(pwd, "program"),
		Config: map[string]string{
			"namespace":        os.Getenv("NAMESPACE"),
			"registry":         os.Getenv("REGISTRY"),
			"tag":              os.Getenv("TAG"),
			"romeo-claim-name": os.Getenv("ROMEO_CLAIM_NAME"),
		},
		Env: []string{
			fmt.Sprintf("GOCOVERDIR=%s", filepath.Join(pwd, "..", "coverdir")),
		},
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			cli := grpcClient(t, stack.Outputs)
			anaCli := analysis.NewAnalysisServiceClient(cli)
			opCli := longrunningpb.NewOperationsClient(cli)

			// Create an analysis (LRO)
			before := time.Now()
			op, err := anaCli.CreateAnalysis(t.Context(), &analysis.CreateAnalysisRequest{
				EdbFacts: strings.Split(`
attackerLocated(internet).
attackGoal(execCode(fileServer, _)).
hacl(internet,    webServer,  tcp, 80).
hacl(webServer,   fileServer, tcp, 445).
hacl(fileServer,  _,          _,   _).
hacl(H,           H,          _,   _).
networkServiceInfo(webServer, httpd, tcp, 80, apache).
vulExists(webServer, 'CVE-2021-44228', httpd).
vulProperty('CVE-2021-44228', remoteExploit, privEscalation).
networkServiceInfo(fileServer, smb, tcp, 445, root).
vulExists(fileServer, 'CVE-2017-0144', smb).
vulProperty('CVE-2017-0144', remoteExploit, privEscalation).
				`, "\n"),
			})
			require.NoError(t, err)
			assert.False(t, op.Done)

			// And wait for it to complete
			res, err := opCli.WaitOperation(t.Context(), &longrunningpb.WaitOperationRequest{
				Name: op.GetName(),
			})
			dur := time.Since(before)
			require.NoError(t, err)
			assert.True(t, res.Done)
			assert.Condition(t, func() (success bool) {
				// Should be no longer than 10 seconds in total, even in CI (takes <1s on host machine)
				return dur < 10*time.Second
			})
		},
	})
}
