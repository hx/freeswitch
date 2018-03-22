package freeswitch_test

import (
	"fmt"
	"github.com/hx/freeswitch"
)

// This example guesses configuration, opens a connection, executes a command, and closes the connection.
func ExampleClient() {
	// Zero clients are valid.
	c := freeswitch.Client{}

	// Guess configuration by attempting to read FreeSWITCH's event socket configuration from the file system.
	c.GuessConfiguration()

	// Open a connection
	if err := c.Open(); err == nil {

		// Run a command and display its result
		fmt.Println(c.Execute("reloadxml"))

		// Close the connection
		c.Close()
	}
	// Output: +OK [Success]
}
