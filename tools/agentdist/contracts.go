package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type toolField struct {
	Kind     string
	Required bool
}

func readMCPToolContracts(repositoryRoot string) (map[string]map[string]toolField, error) {
	definition, err := loadDefinition(repositoryRoot)
	if err != nil {
		return nil, err
	}
	return readMCPToolContractsAt(filepath.Join(repositoryRoot, filepath.FromSlash(definition.MCPRegistrationSource)), definition.ExpectedMCPTools, definition.MCPToolPrefix)
}

func readMCPToolContractsAt(filename string, expectedTools int, toolPrefix string) (map[string]map[string]toolField, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse MCP server registrations: %w", err)
	}
	types := make(map[string]*ast.StructType)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if ok {
				types[typeSpec.Name.Name] = structure
			}
		}
	}
	tools := make(map[string]map[string]toolField)
	invalidToolName := ""
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 3 {
			return true
		}
		function, ok := call.Fun.(*ast.Ident)
		if !ok || function.Name != "add" {
			return true
		}
		literal, ok := call.Args[1].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		shortName, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr != nil {
			return true
		}
		if !mcpToolSuffixPattern.MatchString(shortName) {
			invalidToolName = shortName
			return false
		}
		handler, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
		if !ok || handler.Type.Params == nil || len(handler.Type.Params.List) == 0 {
			return true
		}
		inputName, ok := handler.Type.Params.List[len(handler.Type.Params.List)-1].Type.(*ast.Ident)
		if !ok {
			return true
		}
		fields := map[string]toolField{}
		if collectStructFields(types, inputName.Name, fields, map[string]bool{}) == nil {
			tools[toolPrefix+shortName] = fields
		}
		return true
	})
	if invalidToolName != "" {
		return nil, fmt.Errorf("MCP tool name suffix %q is invalid", invalidToolName)
	}
	if len(tools) != expectedTools {
		return nil, fmt.Errorf("found %d MCP tool registrations, expected %d", len(tools), expectedTools)
	}
	return tools, nil
}

func collectStructFields(types map[string]*ast.StructType, typeName string, fields map[string]toolField, visiting map[string]bool) error {
	structure, found := types[typeName]
	if !found {
		return fmt.Errorf("input type %q is not a struct", typeName)
	}
	if visiting[typeName] {
		return fmt.Errorf("recursive MCP input type %q", typeName)
	}
	visiting[typeName] = true
	defer delete(visiting, typeName)
	for _, field := range structure.Fields.List {
		if len(field.Names) == 0 {
			embedded, ok := field.Type.(*ast.Ident)
			if !ok {
				return fmt.Errorf("unsupported embedded MCP field in %s", typeName)
			}
			if err := collectStructFields(types, embedded.Name, fields, visiting); err != nil {
				return err
			}
			continue
		}
		if field.Tag == nil {
			return fmt.Errorf("MCP input %s.%s has no JSON tag", typeName, field.Names[0].Name)
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return err
		}
		jsonTag := reflect.StructTag(tag).Get("json")
		parts := strings.Split(jsonTag, ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Names[0].Name
		}
		optional := false
		for _, part := range parts[1:] {
			optional = optional || part == "omitempty"
		}
		kind := expressionKind(field.Type)
		if kind == "unknown" {
			return fmt.Errorf("unsupported MCP field type for %s.%s", typeName, field.Names[0].Name)
		}
		contract := toolField{Kind: kind, Required: !optional}
		fields[name] = contract
	}
	return nil
}

func expressionKind(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		switch typed.Name {
		case "string":
			return "string"
		case "bool":
			return "boolean"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			return "number"
		default:
			return "object"
		}
	case *ast.ArrayType:
		return "array"
	case *ast.MapType, *ast.StructType:
		return "object"
	case *ast.StarExpr:
		return expressionKind(typed.X)
	case *ast.InterfaceType:
		return "object"
	default:
		return "unknown"
	}
}

func validateContractExamples(filename string, tools map[string]map[string]toolField) error {
	body, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read MCP contract examples: %w", err)
	}
	examples := map[string]map[string]json.RawMessage{}
	if err = decodeStrict(body, &examples); err != nil {
		return fmt.Errorf("invalid MCP contract examples: %w", err)
	}
	if len(examples) != len(tools) {
		return fmt.Errorf("MCP contract examples cover %d tools, expected %d", len(examples), len(tools))
	}
	for toolName, fields := range examples {
		contract, found := tools[toolName]
		if !found {
			return fmt.Errorf("contract example references unregistered MCP tool %q", toolName)
		}
		for name, field := range contract {
			if field.Required {
				if _, found = fields[name]; !found {
					return fmt.Errorf("contract example for %s omits required field %q", toolName, name)
				}
			}
		}
		for name, raw := range fields {
			field, found := contract[name]
			if !found {
				return fmt.Errorf("contract example for %s contains unsupported argument %q", toolName, name)
			}
			var value any
			if err = json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("contract example %s.%s is invalid JSON: %w", toolName, name, err)
			}
			if !jsonValueMatchesKind(value, field.Kind) {
				return fmt.Errorf("contract example %s.%s has the wrong JSON type; expected %s", toolName, name, field.Kind)
			}
		}
	}
	return nil
}

func jsonValueMatchesKind(value any, kind string) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func readCLICommands(repositoryRoot string) (map[string]bool, error) {
	definition, err := loadDefinition(repositoryRoot)
	if err != nil {
		return nil, err
	}
	return readCLICommandsAt(filepath.Join(repositoryRoot, filepath.FromSlash(definition.CLIHelpSource)), definition.CLIHelpConstant, definition.CLIName)
}

func readCLICommandsAt(filename, helpConstant, cliName string) (map[string]bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse CLI help: %w", err)
	}
	help := ""
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != helpConstant || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return nil, fmt.Errorf("CLI help constant %s is not a string literal", helpConstant)
			}
			help, err = strconv.Unquote(literal.Value)
			if err != nil {
				return nil, err
			}
		}
	}
	if help == "" {
		return nil, fmt.Errorf("CLI help constant %s was not found", helpConstant)
	}
	commands := map[string]bool{}
	commands["help"] = true
	for _, match := range cliCommandReferencePatternFor(cliName).FindAllStringSubmatch(help, -1) {
		if len(match) == 2 {
			commands[match[1]] = true
		}
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("no CLI commands found in %s help", cliName)
	}
	return commands, nil
}
