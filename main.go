/*
This file is:

The MIT License (MIT)

Copyright (c) 2014 Bitrise

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"os"
	"strings"
	"regexp"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-tools/go-steputils/stepconf"
)

// Config ...
type Config struct {
	// Settings
	Debug      bool            `env:"is_debug_mode,opt[yes,no]"`
	WebhookURL stepconf.Secret `env:"webhook_url"`
	// Message Main
	ThemeColor        string `env:"theme_color"`
	ThemeColorOnError string `env:"theme_color_on_error"`
	Title             string `env:"title"`
	TitleOnError      string `env:"title_on_error"`
	// Message Git
	AuthorName string `env:"author_name"`
	Subject    string `env:"subject"`
	// Message Content
	Fields         string `env:"fields"`
	Images         string `env:"images"`
	ImagesOnError  string `env:"images_on_error"`
	Buttons        string `env:"buttons"`
	ButtonsOnError string `env:"buttons_on_error"`
}

// success is true if the build is successful, false otherwise.
var success = os.Getenv("BITRISE_BUILD_STATUS") == "0"

// selectValue chooses the right value based on the result of the build.
func selectValue(ifSuccess, ifFailed string) string {
	if success || ifFailed == "" {
		return ifSuccess
	}
	return ifFailed
}

// ensureNewlines replaces all \n substrings with newline characters.
func ensureNewlines(s string) string {
	return strings.Replace(s, "\\n", "\n", -1)
}

func runShellCommand(cli_command string) (string, error) {
	args := strings.Fields(cli_command)
	cmd := exec.Command(args[0], args[1:]...)

	// Set the working directory
	cmd.Dir = "."

	// Run the command
	output, err := cmd.CombinedOutput()
	// Idea, on error, always fallback to given value
	if err != nil {
		fmt.Printf("Error: %s with output: %s\n", err, output)
		return "", err
	}

	// Print the output
	fmt.Println(string(output))
	return string(output), nil
}

// extractCommand extracts the command and its flags from a string
// that contains subshell syntax like '"$(command)"'.
func extractCommand(input string) string {
	// Trim the leading and trailing quotes
	trimmed := strings.Trim(input, `'"`)

	// Remove the subshell syntax '$(...)'
	return strings.TrimPrefix(strings.TrimSuffix(trimmed, ")"), "$(")
}

func determineMessageValue(given_value string) string {
	if strings.HasPrefix(given_value, "$(") && strings.HasSuffix(given_value, ")") {
		shell_cmd := extractCommand(given_value)
		value, _ := runShellCommand(shell_cmd)
		return value
	} else {
		return given_value
	}
}

// resolveSubshellCommands finds and executes subshell commands in a string.
func resolveSubshellCommands(input string) (string, error) {
	// Regular expression to find subshell command patterns
	re := regexp.MustCompile(`\$\((.*?)\)`)

	// Find all matches
	matches := re.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		fullMatch := match[0]
		command := match[1]

		// Run the subshell command
		output, err := runShellCommand(command)
		if err != nil {
			return "", err
		}

		// Replace the subshell command in the original string with its output
		input = strings.Replace(input, fullMatch, strings.TrimSpace(output), 1)
	}

	return input, nil
}

func newMessage(c Config) Message {
	fmt.Printf("Config: %s\n", c)
	author, _ := resolveSubshellCommands(c.AuthorName)
	fmt.Printf("Found author: %s\n", author)

	title, _ := resolveSubshellCommands(c.Title)//determineMessageValue(c.Title)
	titleOnError, _ := resolveSubshellCommands(c.TitleOnError)

	subject, _ := resolveSubshellCommands(c.Subject)

	fields, err := resolveSubshellCommands(c.Fields)
	if err != nil {
		fmt.Printf("Error parsing fields string: %s\n", err)
	}
	fmt.Printf("Resolved fields: %s\n", fields)

	buttons, _ := resolveSubshellCommands(c.Buttons)
	buttonsOnError, _ := resolveSubshellCommands(c.ButtonsOnError)

	msg := Message{
		Context:    "https://schema.org/extension",
		Type:       "MessageCard",
		ThemeColor: selectValue(c.ThemeColor, c.ThemeColorOnError),
		Title:      selectValue(title, titleOnError),
		Summary:    "Result of Bitrise",
		Sections: []Section{{
			ActivityTitle: author,
			ActivityText:  ensureNewlines(subject),
			Facts:         parsesFacts(fields),
			Images:        parsesImages(selectValue(c.Images, c.ImagesOnError)),
			Actions:       parsesActions(selectValue(buttons, buttonsOnError)),
		}},
	}

	return msg
}

// postMessage sends a message.
func postMessage(conf Config, msg Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	log.Debugf("Post Json Data: %s\n", b)

	url := determineMessageValue(string(conf.WebhookURL))
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send the request: %s", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); err == nil {
			err = cerr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("server error: %s, failed to read response: %s", resp.Status, err)
		}
		return fmt.Errorf("server error: %s, response: %s", resp.Status, body)
	}

	return nil
}

func main() {
	var conf Config
	if err := stepconf.Parse(&conf); err != nil {
		log.Errorf("Error: %s\n", err)
		os.Exit(1)
	}
	stepconf.Print(conf)
	log.SetEnableDebugLog(conf.Debug)

	msg := newMessage(conf)
	if err := postMessage(conf, msg); err != nil {
		log.Errorf("Error: %s", err)
		os.Exit(1)
	}

	log.Donef("\nMessage successfully sent! 🚀\n")
}
