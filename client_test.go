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

// This example shows waiting for connection to succeed before attempting to run a command.
//
// Because Connect() blocks until disconnection, intended or otherwise, there's no clear indication that
// connection and handshaking has completed. However, Execute() blocks until the client is ready to send commands,
// so a simple no-op can guide your way.
func ExampleClient_Execute() {
	// Make a new client, guessing its configuration
	c := freeswitch.NewClient()

	// Open a connection, with no regard to how it terminates
	go c.Connect()

	// A successful execution means connection has succeeded
	if _, err := c.Execute("status"); err != nil {
		panic(err)
	}

	// We can now assume we are connected, and go about our business
	fmt.Println(c.Execute("reloadxml"))
	c.Shutdown()
	// Output: +OK [Success]
}
