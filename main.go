package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ANSI Color codes
const (
	colorCyan   = "\033[36m"
	// colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

// Config holds the application configuration
type Config struct {
	APIEndpoint string `toml:"api_endpoint"`
	APIKey      string `toml:"api_key"`
	Model       string `toml:"model"`
	Username    string `toml:"username"`
}

// API request and response structures
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// Structures for streaming response
type StreamChoice struct {
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason interface{} `json:"finish_reason"`
}
type StreamResponse struct {
	Choices []StreamChoice `json:"choices"`
}

func main() {
	// Load configuration
	config, err := loadOrInitConfig()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		return
	}

	fmt.Println("Welcome to gogpt! Type 'exit', 'quit', or press Ctrl+D to end the chat.")
	fmt.Printf("Connected to model '%s' at '%s'.\n\n", config.Model, config.APIEndpoint)
	fmt.Println("  _______   ______     _______ .______   .___________.")
        fmt.Println(" /  _____| /  __  \\   /  _____||   _  \\  |           |")
        fmt.Println("|  |  __  |  |  |  | |  |  __  |  |_)  | `---|  |----`")
        fmt.Println("|  | |_ | |  |  |  | |  | |_ | |   ___/      |  |     ")
        fmt.Println("|  |__| | |  `--'  | |  |__| | |  |          |  |     ")
        fmt.Println(" \\______|  \\______/   \\______| | _|          |__|   \n")

	// Initialize conversation history with a system message
	conversationHistory := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant."},
	}

	// Start interactive chat loop
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s%s:%s ", colorYellow, config.Username, colorReset)
		userInput, err := reader.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				fmt.Println("\nGoodbye!")
				break
			}
			fmt.Printf("\nError reading input: %v\n", err)
			break
		}

		userInput = strings.TrimSpace(userInput)

		if strings.ToLower(userInput) == "exit" || strings.ToLower(userInput) == "quit" {
			fmt.Println("Goodbye!")
			break
		}
		if userInput == "" {
			continue
		}

		// Add user message to history
		conversationHistory = append(conversationHistory, ChatMessage{Role: "user", Content: userInput})

		// Get streaming response from the AI
		fullResponse, err := streamChatResponse(config, conversationHistory)
		if err != nil {
			fmt.Printf("Error getting response: %v\n", err)
			// Remove the last user message if the API call failed
			conversationHistory = conversationHistory[:len(conversationHistory)-1]
			continue
		}

		// Add the full response to history for context in the next turn
		conversationHistory = append(conversationHistory, ChatMessage{Role: "assistant", Content: fullResponse})
	}
}

// streamChatResponse handles the streaming API call and prints the response token-by-token.
func streamChatResponse(config *Config, messages []ChatMessage) (string, error) {
	chatEndpoint := fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(config.APIEndpoint, "/"))

	requestBody := ChatRequest{
		Model:    config.Model,
		Messages: messages,
		Stream:   true,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("could not marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", chatEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("could not create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("received non-OK HTTP status: %s, Body: %s", resp.Status, string(bodyBytes))
	}

	// Prepare to print the response
	fmt.Printf("\n%sGoGPT:%s\n%s", colorCyan, colorReset, colorReset)
	var fullResponse strings.Builder
	streamReader := bufio.NewReader(resp.Body)

	for {
		line, err := streamReader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("error reading stream: %w", err)
		}
		line = strings.TrimSpace(line)

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var streamResp StreamResponse
		if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
			// Skip lines that are not valid JSON
			continue
		}

		if len(streamResp.Choices) > 0 {
			content := streamResp.Choices[0].Delta.Content
			fmt.Print(content)
			fullResponse.WriteString(content)
			// Check for finish reason to stop early if needed
			if streamResp.Choices[0].FinishReason != nil {
				break
			}
		}
	}

	// Reset color and add spacing after the stream is complete
	fmt.Printf("%s\n\n", colorReset)

	return fullResponse.String(), nil
}

// loadOrInitConfig loads config from file or prompts user for initial setup.
func loadOrInitConfig() (*Config, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("could not get current user: %w", err)
	}
	configDir := filepath.Join(usr.HomeDir, ".config", "gogpt")
	configPath := filepath.Join(configDir, "config.toml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Println("Configuration file not found. Let's set it up.")
		return promptForConfig(configDir, configPath)
	}

	var config Config
	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return nil, fmt.Errorf("could not decode config file: %w", err)
	}

	// Handle case where username is missing from older configs
	if config.Username == "" {
		config.Username = "User"
	}

	fmt.Printf("Configuration loaded from %s\n", configPath)
	return &config, nil
}

// promptForConfig interacts with the user to create the initial config file.
func promptForConfig(configDir, configPath string) (*Config, error) {
	reader := bufio.NewReader(os.Stdin)

	// Prompt for API Endpoint
	fmt.Print("Enter API Endpoint URL [http://127.0.0.1:8080/v1]: ")
	endpoint, _ := reader.ReadString('\n')
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080/v1"
	}

	// Prompt for API Key (optional)
	fmt.Print("Enter API Key (optional, press Enter to skip): ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	// Prompt for Model Name
	fmt.Print("Enter Model Name [gpt-oss-20b]: ")
	model, _ := reader.ReadString('\n')
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gpt-oss-20b"
	}

	// Prompt for Username
	fmt.Print("Enter your name to be displayed [User]: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)
	if username == "" {
		username = "User"
	}

	config := &Config{
		APIEndpoint: endpoint,
		APIKey:      apiKey,
		Model:       model,
		Username:    username,
	}

	// Save the new configuration
	if err := saveConfig(config, configDir, configPath); err != nil {
		return nil, err
	}

	fmt.Printf("Configuration saved to %s\n", configPath)
	return config, nil
}

// saveConfig saves the config struct to the specified TOML file.
func saveConfig(config *Config, configDir, configPath string) error {
	if err := os.MkdirAll(configDir, 0750); err != nil {
		return fmt.Errorf("could not create config directory: %w", err)
	}

	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("could not create config file: %w", err)
	}
	defer file.Close()

	if err := toml.NewEncoder(file).Encode(config); err != nil {
		return fmt.Errorf("could not encode config to file: %w", err)
	}
	return nil
}
