// This file is part of arduino-router
//
// Copyright 2025 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/arduino/arduino-router/msgpackrpc"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func main() {
	var notification bool
	var server string
	appname := os.Args[0]
	cmd := cobra.Command{
		Short: "Send a MsgPack RPC REQUEST or NOTIFICATION.",
		Use: appname + " [flags] <METHOD> [<ARG> [<ARG> ...]]\n\n" +
			"Send REQUEST:      " + appname + " [-s server_addr]    <METHOD> [<ARG> [<ARG> ...]]\n" +
			"Send NOTIFICATION: " + appname + " [-s server_addr] -n <METHOD> [<ARG> [<ARG> ...]]\n\n" +
			"  <METHOD> is the method name to request/notify,\n" +
			"  <ARG> are the arguments to pass to the method:\n" +
			"      - Use 'true', 'false' for boolean\n" +
			"      - Use 'null' for null.\n" +
			"      - Use integer values directly (e.g., 42).\n" +
			"      - Use the 'f32:' or 'f64:' prefix for floating point values (e.g. f32:3.14159).\n" +
			"      - Use '[' and ']' to start and end an array.\n" +
			"      - Use '{' and '}' to start and end a map.\n" +
			"        Keys and values should be listed in order { KEY1 VAL1 KEY2 VAL2 }.\n" +
			"      - Any other value is treated as a string, or you may use the 'str:' prefix\n" +
			"        explicitly in case of ambiguity (e.g. str:42)",
		Run: func(cmd *cobra.Command, cliArgs []string) {
			// Compose method and arguments
			args, rest, err := composeArgs(append(append([]string{"["}, cliArgs[1:]...), "]"))
			if err != nil {
				fmt.Println("Invalid arguments:", err)
				os.Exit(1)
			}
			if len(rest) > 0 {
				fmt.Println("Invalid arguments: extra data:", rest)
				os.Exit(1)
			}
			if _, ok := args.([]any); !ok {
				fmt.Println("Invalid arguments: expected array")
				os.Exit(1)
			}

			yamlEncoder := yaml.NewEncoder(os.Stdout)
			yamlEncoder.SetIndent(2)
			fmt.Println("Sending parameters:")
			yamlEncoder.Encode(args)

			// Perfom request send
			if rpcResp, rpcErr, err := send(server, cliArgs[0], args.([]any), notification); err != nil {
				fmt.Println("Error sending request:", err)
				os.Exit(1)
			} else {
				if !notification {
					yamlEncoder := yaml.NewEncoder(os.Stdout)
					yamlEncoder.SetIndent(2)
					if rpcErr != nil {
						fmt.Println("Got RPC error response:")
						yamlEncoder.Encode(rpcErr)
					}
					if rpcResp != nil {
						fmt.Println("Got RPC response:")
						yamlEncoder.Encode(rpcResp)
					}
				}
			}
		},
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
	}
	cmd.Flags().BoolVarP(
		&notification, "notification", "n", false,
		"Send a NOTIFICATION instead of a CALL")
	cmd.Flags().StringVarP(
		&server, "server", "s", "/var/run/arduino-router.sock",
		"Server address (file path for unix socket)")
	if err := cmd.Execute(); err != nil {
		fmt.Printf("Use: %s -h for help.\n", appname)
		os.Exit(1)
	}
}

func composeArgs(args []string) (any, []string, error) {
	if len(args) == 0 {
		return nil, nil, fmt.Errorf("no arguments provided")
	}
	if args[0] == "null" {
		return nil, args[1:], nil
	}
	if args[0] == "true" {
		return true, args[1:], nil
	}
	if args[0] == "false" {
		return false, args[1:], nil
	}
	if f32, ok := strings.CutPrefix(args[0], "f32:"); ok {
		f, err := strconv.ParseFloat(f32, 32)
		if err != nil {
			return nil, args, fmt.Errorf("invalid f32 value: %s", args[0])
		}
		return float32(f), args[1:], nil
	}
	if f64, ok := strings.CutPrefix(args[0], "f64:"); ok {
		f, err := strconv.ParseFloat(f64, 64)
		if err != nil {
			return nil, args, fmt.Errorf("invalid f64 value: %s", args[0])
		}
		return f, args[1:], nil
	}
	if str, ok := strings.CutPrefix(args[0], "str:"); ok {
		return str, args[1:], nil
	}
	if args[0] == "[" {
		arr := []any{}
		rest := args[1:]
		for {
			if len(rest) == 0 {
				return nil, args, fmt.Errorf("unterminated array")
			}
			if rest[0] == "]" {
				break
			}
			if elem, r, err := composeArgs(rest); err != nil {
				return nil, args, err
			} else {
				arr = append(arr, elem)
				rest = r
			}
		}
		return arr, rest[1:], nil
	}
	if args[0] == "{" {
		m := make(map[any]any)
		rest := args[1:]
		for {
			if len(rest) == 0 {
				return nil, args, fmt.Errorf("unterminated map")
			}
			if rest[0] == "}" {
				break
			}
			key, r, err := composeArgs(rest)
			if err != nil {
				return nil, args, fmt.Errorf("invalid map key: %w", err)
			}
			rest = r
			if len(rest) == 0 {
				return nil, args, fmt.Errorf("unterminated map (missing value)")
			}
			value, r, err := composeArgs(rest)
			if err != nil {
				return nil, args, fmt.Errorf("invalid map value: %w", err)
			}
			m[key] = value
			rest = r
		}
		return m, rest[1:], nil
	}
	// Autodetect int or string
	if i, err := strconv.Atoi(args[0]); err == nil {
		return i, args[1:], nil
	}
	return args[0], args[1:], nil
}

func send(server string, method string, args []any, notification bool) (any, any, error) {
	netType := "unix"
	if strings.Contains(server, ":") {
		netType = "tcp"
	}
	c, err := net.Dial(netType, server)
	if err != nil {
		return nil, nil, fmt.Errorf("error connecting to server: %w", err)
	}

	conn := msgpackrpc.NewConnection(c, c, nil, nil, nil)
	defer conn.Close()
	go conn.Run()

	// Send notification...
	if notification {
		if err := conn.SendNotification(method, args); err != nil {
			return nil, nil, fmt.Errorf("error sending notification: %w", err)
		}
		return nil, nil, nil
	}

	// ...or send Request
	reqResult, reqError, err := conn.SendRequest(context.Background(), method, args)
	if err != nil {
		return nil, nil, fmt.Errorf("error sending request: %w", err)
	}
	return reqResult, reqError, nil
}
