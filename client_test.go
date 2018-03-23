package freeswitch_test

import (
	"fmt"
	"github.com/hx/freeswitch"
	"os/exec"
	"time"
)

// This example opens a connection, executes a command, and closes the connection.
func ExampleNewClient_simple() {
	// Make a new client, guessing its configuration
	c := freeswitch.NewClient()

	// Open a connection
	go c.Connect()

	// Run a command and display its result
	fmt.Println(c.MustExecute("reloadxml"))

	// Close the connection
	c.Shutdown()
	// Output: +OK [Success]
}

// This example tries to persist the connection after failures.
func ExampleNewClient_persistent() {
	// Make a new client, guessing its configuration
	c := freeswitch.NewClient()

	// Connect to FreeSWITCH, and as long as connections terminate abnormally, keep reconnecting:
	go func() {
		for err := c.Connect(); err != nil; {
			fmt.Printf("Disconnected: %s; trying again in 5 seconds", err)
			time.Sleep(5 * time.Second)
		}
		fmt.Println("Termination was graceful.")
	}()

	// Let the connection run a while
	time.Sleep(3 * time.Second)

	// Disrupt the natural order
	exec.Command("pkill -9 freeswitch").Run()

	// Allow FreeSWITCH (running as a service) to restart, and the client to reconnect
	time.Sleep(10 * time.Second)

	// Shut down normally
	c.Shutdown()
	// Output: Disconnected: EOF; trying again in 5 seconds
	// Termination was graceful.
}
