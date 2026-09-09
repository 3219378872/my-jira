package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/application"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/openapi"
	"my-jira/apps/api/internal/platform"
)

func main() {
	output := flag.String("output", "", "write generated OpenAPI JSON to this file; defaults to stdout")
	flag.Parse()
	gin.SetMode(gin.ReleaseMode)
	router := application.Router(platform.Dependencies{}, foundation.Config{})
	document, err := openapi.Build(router.Routes())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw = append(raw, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(raw)
	} else {
		err = os.WriteFile(*output, raw, 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
