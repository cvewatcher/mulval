package global

import "github.com/cvewatcher/mulval/pkg/config"

var (
	Version = "dev"
)

var Config = config.New() // Default so it avoids nil-pointer dereference on root-level errors
