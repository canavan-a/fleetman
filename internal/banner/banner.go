package banner

import "fmt"

const Art = `
  __ _           _
 / _| | ___  ___| |_ _ __ ___   __ _ _ __
| |_| |/ _ \/ _ \ __| '_ ` + "`" + ` _ \ / _` + "`" + ` | '_ \
|  _| |  __/  __/ |_| | | | | | (_| | | | |
|_| |_|\___|\___|\__|_| |_| |_|\__,_|_| |_|
`

// Print prints the banner with an optional version string.
func Print(version string) {
	fmt.Print(Art)
	if version != "" {
		fmt.Printf("  %s\n", version)
	}
	fmt.Println()
}
