package freeswitch

import (
	"encoding/xml"
	"io"
	"os"
	"strconv"
	"strings"
)

var standardConfigPaths = []string{
	"/etc/freeswitch/autoload_configs/event_socket.conf.xml",
}

// Try to guess the event socket configuration based on the standard installation paths of FreeSWITCH's configuration.
func (c *Client) guessConfiguration() {
	for _, path := range standardConfigPaths {
		if c.ReadConfiguration(path) == nil {
			return
		}
	}
}

// Read event socket configuration (host, port, and password) from the event socket configuration file (should
// be event_socket.conf.xml).
func (c *Client) ReadConfiguration(freeswitchConfPath string) error {
	file, err := os.Open(freeswitchConfPath)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	var (
		values = make(map[string]string)
		key    string
	)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			key = token.Name.Local
			if !(key == "listen-port" || key == "listen-ip" || key == "password") {
				key = ""
			}
		case xml.CharData:
			if key != "" {
				values[key] = strings.TrimSpace(string(token))
			}
		default:
			key = ""
		}
		if len(values) == 3 {
			break
		}
	}
	for k, v := range values {
		switch k {
		case "listen-port":
			if v, err := strconv.Atoi(v); err == nil {
				c.Port = uint16(v)
			} else {
				return err
			}
		case "listen-ip":
			c.Hostname = v
		case "password":
			c.Password = v
		}
	}
	return nil
}
