package integrations

import "fmt"

func exportsDeleted(filters map[string]any) bool {
	value := fmt.Sprint(filters["deleted"])
	return value == "true" || value == "all"
}
