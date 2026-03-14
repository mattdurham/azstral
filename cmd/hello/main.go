// Package main is the hello world test application for azstral.
// SPEC-004: Print "Hello World" to the console.
// NOTE-004: This program serves as both a test fixture and proof of concept.
package main

import (
	"fmt"
)

// main prints Hello World to stdout.
// SPEC-004: The application should print "Hello World" to the console.
// TEST-006: Verify output is "Hello World".
func main() {
	fmt.Println("Hello World!")
}
