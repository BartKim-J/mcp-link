package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func sanitizeToolName(name string) string {
	// Convert to lowercase and replace common separators with underscore
	s := strings.ToLower(name)

	// Replace special characters
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "?", "")
	s = strings.ReplaceAll(s, "&", "and")
	s = strings.ReplaceAll(s, "=", "_eq_")
	s = strings.ReplaceAll(s, "%", "_pct_")

	// Remove consecutive underscores
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}

	// Remove trailing underscore
	s = strings.TrimSuffix(s, "_")

	// Remove leading underscore
	s = strings.TrimPrefix(s, "_")

	// Ensure the name is not empty
	if s == "" {
		return "unnamed_tool"
	}

	return s
}

func NewToolHandler(method string, url string, extraHeaders map[string]string) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Extract parameters from the request
		params := request.Params.Arguments

		// Create maps for different parameter types
		pathParams := make(map[string]interface{})
		queryParams := make(map[string]interface{})
		bodyParams := make(map[string]interface{})

		// Extract specific parameter groups
		if pathParamsMap, ok := params["pathNames"].(map[string]interface{}); ok {
			pathParams = pathParamsMap
		}

		if urlParamsMap, ok := params["searchParams"].(map[string]interface{}); ok {
			queryParams = urlParamsMap
		}

		if requestBodyMap, ok := params["requestBody"].(map[string]interface{}); ok {
			bodyParams = requestBodyMap
		}

		// If structured params aren't found, use flat params for backward compatibility
		if len(pathParams) == 0 && len(queryParams) == 0 && len(bodyParams) == 0 {
			// Process all params without structured separation (legacy approach)
			for paramName, paramValue := range params {
				placeholder := fmt.Sprintf("{%s}", paramName)
				if strings.Contains(url, placeholder) {
					pathParams[paramName] = paramValue
				} else {
					// Put in body by default
					bodyParams[paramName] = paramValue
				}
			}
		}

		// Create a copy of the URL for path parameter substitution
		finalURL := url

		// Process URL path parameters - replace {param_name} with the value from pathParams
		for paramName, paramValue := range pathParams {
			placeholder := fmt.Sprintf("{%s}", paramName)
			if strings.Contains(finalURL, placeholder) {
				// Convert the param value to string
				var strValue string
				switch v := paramValue.(type) {
				case string:
					strValue = v
				case nil:
					// Use empty string for nil path parameters
					strValue = ""
				default:
					// Convert other types to string
					strValue = fmt.Sprintf("%v", v)
				}

				// Replace the placeholder in the URL
				finalURL = strings.ReplaceAll(finalURL, placeholder, strValue)
			}
		}
		// Add query parameters to the URL
		if len(queryParams) > 0 {
			// Parse the URL to add query parameters properly
			parsedURL, err := neturl.Parse(finalURL)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("Error parsing URL: %v", err)), nil
			}

			// Get existing query values or create new ones
			q := parsedURL.Query()

			// Add all query parameters
			for paramName, paramValue := range queryParams {
				// Convert the param value to string
				var strValue string
				switch v := paramValue.(type) {
				case string:
					strValue = v
				case nil:
					continue
				default:
					// Convert other types to string
					strValue = fmt.Sprintf("%v", v)
				}

				q.Add(paramName, strValue)
			}

			// Set the updated query string back to the URL
			parsedURL.RawQuery = q.Encode()
			finalURL = parsedURL.String()
		}

		// Convert body parameters to JSON for the HTTP request body
		var reqBody io.Reader = nil
		if len(bodyParams) > 0 {
			jsonParams, err := json.Marshal(bodyParams)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("Error marshaling body parameters: %v", err)), nil
			}
			reqBody = bytes.NewBuffer(jsonParams)
		}

		// Create HTTP request with the processed URL
		req, err := http.NewRequestWithContext(ctx, method, finalURL, reqBody)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error creating request: %v", err)), nil
		}

		// Set headers
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		// Execute the request
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error executing request: %v", err)), nil
		}
		defer resp.Body.Close()

		// Read response body
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error reading response: %v", err)), nil
		}

		// TODO: handle image response
		// if strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		// return mcp.NewToolResultImage("", base64.StdEncoding.EncodeToString(body), resp.Header.Get("Content-Type")), nil
		// }

		return mcp.NewToolResultText(string(body)), nil
	}
}

// NewMCPFromCustomParser creates an MCP server from our custom OpenAPIParser
func NewMCPFromCustomParser(baseURL string, extraHeaders map[string]string, parser OpenAPIParser) (*server.MCPServer, error) {
	// Create a new MCP server
	apiInfo := parser.Info()
	prefix := "mcplink_" + sanitizeToolName(apiInfo.Title)

	s := server.NewMCPServer(
		prefix,
		apiInfo.Version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
	)

	// Add all API endpoints as tools
	for _, api := range parser.APIs() {
		name := sanitizeToolName(fmt.Sprintf("%s_%s", prefix, api.OperationID))
		opts := []mcp.ToolOption{
			mcp.WithDescription(api.OperationID + " " + api.Summary + " " + api.Description),
		}

		query_props := map[string]interface{}{}
		path_props := map[string]interface{}{}

		for _, param := range api.Parameters {
			prop := map[string]interface{}{
				"type": param.Schema.Type,
				"description": param.Description,
			}
			if param.Schema.Enum != nil {
				prop["enum"] = param.Schema.Enum
			}
			if param.Schema.Format != "" {
				prop["format"] = param.Schema.Format
			}
			if param.Schema.Default != nil {
				prop["default"] = param.Schema.Default
			}
			if param.Schema.Items != nil {
				prop["items"] = param.Schema.Items
			}
			if param.Schema.Properties != nil {
				prop["properties"] = param.Schema.Properties
			}
			if param.Required {
				prop["required"] = true
			}

			switch param.In {
			case "query":
				query_props[param.Name] = prop
			case "path":
				path_props[param.Name] = prop
			}
		}

		if len(query_props) > 0 {
			opts = append(opts, mcp.WithObject("searchParams", mcp.Description("url parameters for the tool"), mcp.Properties(query_props)))
		}
		if len(path_props) > 0 {
			opts = append(opts, mcp.WithObject("pathNames", mcp.Description("path parameters for the tool"), mcp.Properties(path_props)))
		}

		props := map[string]interface{}{}
		if api.RequestBody != nil && len(api.RequestBody.Content) > 0 {
			for _, mediaType := range api.RequestBody.Content {
				if mediaType.Schema != nil {
					for propName, propSchema := range mediaType.Schema.Properties {
						prop := map[string]interface{}{
							"type":        propSchema.Type,
							"description": propSchema.Description,
						}
						if propSchema.Enum != nil {
							prop["enum"] = propSchema.Enum
						}
						if propSchema.Format != "" {
							prop["format"] = propSchema.Format
						}
						if propSchema.Default != nil {
							prop["default"] = propSchema.Default
						}
						if propSchema.Items != nil {
							prop["items"] = propSchema.Items
						}
						if propSchema.Properties != nil {
							prop["properties"] = propSchema.Properties
						}
						props[propName] = prop
					}
				}
			}
			opts = append(opts, mcp.WithObject("requestBody", mcp.Description("request body for the tool"), mcp.Properties(props)))
		}

		tool := mcp.NewTool(name, opts...)
		handler := NewToolHandler(api.Method, baseURL+api.Path, extraHeaders)
		s.AddTool(tool, handler)
	}

	return s, nil
}
