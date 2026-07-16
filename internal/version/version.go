package version

import (
	_ "embed"
	"strings"
)

//go:embed build.txt
var buildText string

func Build() string {
	build := strings.TrimSpace(buildText)
	if build == "" {
		return "0"
	}
	return build
}

func Label() string {
	return "Repo Tool build " + Build()
}
