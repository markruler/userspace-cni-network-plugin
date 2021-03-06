// Copyright (c) 2017 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// generatedCodeVersion indicates a version of the generated code.
// It is incremented whenever an incompatibility between the generated code and
// GoVPP api package is introduced; the generated code references
// a constant, api.GoVppAPIPackageIsVersionN (where N is generatedCodeVersion).
const generatedCodeVersion = 1

const (
	inputFileExt  = ".api.json" // file extension of the VPP API files
	outputFileExt = ".ba.go"    // file extension of the Go generated files

	govppApiImportPath = "git.fd.io/govpp.git/api" // import path of the govpp API package

	constModuleName = "ModuleName" // module name constant
	constAPIVersion = "APIVersion" // API version constant
	constVersionCrc = "VersionCrc" // version CRC constant

	unionDataField = "XXX_UnionData" // name for the union data field
)

// context is a structure storing data for code generation
type context struct {
	inputFile  string // input file with VPP API in JSON
	outputFile string // output file with generated Go package

	inputData []byte // contents of the input file

	includeAPIVersion  bool // include constant with API version string
	includeComments    bool // include parts of original source in comments
	includeBinapiNames bool // include binary API names as struct tag
	includeServices    bool // include service interface with client implementation

	moduleName  string // name of the source VPP module
	packageName string // name of the Go package being generated

	packageData *Package // parsed package data
}

// getContext returns context details of the code generation task
func getContext(inputFile, outputDir string) (*context, error) {
	if !strings.HasSuffix(inputFile, inputFileExt) {
		return nil, fmt.Errorf("invalid input file name: %q", inputFile)
	}

	ctx := &context{
		inputFile: inputFile,
	}

	// package name
	inputFileName := filepath.Base(inputFile)
	ctx.moduleName = inputFileName[:strings.Index(inputFileName, ".")]

	// alter package names for modules that are reserved keywords in Go
	switch ctx.moduleName {
	case "interface":
		ctx.packageName = "interfaces"
	case "map":
		ctx.packageName = "maps"
	default:
		ctx.packageName = ctx.moduleName
	}

	// output file
	packageDir := filepath.Join(outputDir, ctx.packageName)
	outputFileName := ctx.packageName + outputFileExt
	ctx.outputFile = filepath.Join(packageDir, outputFileName)

	return ctx, nil
}

// generatePackage generates code for the parsed package data and writes it into w
func generatePackage(ctx *context, w io.Writer) error {
	logf("generating package %q", ctx.packageName)

	// generate file header
	generateHeader(ctx, w)
	generateImports(ctx, w)

	// generate module desc
	fmt.Fprintln(w, "const (")
	fmt.Fprintf(w, "\t// %s is the name of this module.\n", constModuleName)
	fmt.Fprintf(w, "\t%s = \"%s\"\n", constModuleName, ctx.moduleName)

	if ctx.includeAPIVersion {
		if ctx.packageData.Version != "" {
			fmt.Fprintf(w, "\t// %s is the API version of this module.\n", constAPIVersion)
			fmt.Fprintf(w, "\t%s = \"%s\"\n", constAPIVersion, ctx.packageData.Version)
		}
		fmt.Fprintf(w, "\t// %s is the CRC of this module.\n", constVersionCrc)
		fmt.Fprintf(w, "\t%s = %v\n", constVersionCrc, ctx.packageData.CRC)
	}
	fmt.Fprintln(w, ")")
	fmt.Fprintln(w)

	// generate enums
	if len(ctx.packageData.Enums) > 0 {
		fmt.Fprintf(w, "/* Enums */\n\n")

		for _, enum := range ctx.packageData.Enums {
			generateEnum(ctx, w, &enum)
		}
	}

	// generate aliases
	if len(ctx.packageData.Aliases) > 0 {
		fmt.Fprintf(w, "/* Aliases */\n\n")

		for _, alias := range ctx.packageData.Aliases {
			generateAlias(ctx, w, &alias)
		}
	}

	// generate types
	if len(ctx.packageData.Types) > 0 {
		fmt.Fprintf(w, "/* Types */\n\n")

		for _, typ := range ctx.packageData.Types {
			generateType(ctx, w, &typ)
		}
	}

	// generate unions
	if len(ctx.packageData.Unions) > 0 {
		fmt.Fprintf(w, "/* Unions */\n\n")

		for _, union := range ctx.packageData.Unions {
			generateUnion(ctx, w, &union)
		}
	}

	// generate messages
	if len(ctx.packageData.Messages) > 0 {
		fmt.Fprintf(w, "/* Messages */\n\n")

		for _, msg := range ctx.packageData.Messages {
			generateMessage(ctx, w, &msg)
		}

		// generate message registrations
		fmt.Fprintln(w, "func init() {")
		for _, msg := range ctx.packageData.Messages {
			name := camelCaseName(msg.Name)
			fmt.Fprintf(w, "\tapi.RegisterMessage((*%s)(nil), \"%s\")\n", name, ctx.moduleName+"."+name)
		}
		fmt.Fprintln(w, "}")
		fmt.Fprintln(w)

		// generate list of messages
		fmt.Fprintf(w, "// Messages returns list of all messages in this module.\n")
		fmt.Fprintln(w, "func AllMessages() []api.Message {")
		fmt.Fprintln(w, "\treturn []api.Message{")
		for _, msg := range ctx.packageData.Messages {
			name := camelCaseName(msg.Name)
			fmt.Fprintf(w, "\t(*%s)(nil),\n", name)
		}
		fmt.Fprintln(w, "}")
		fmt.Fprintln(w, "}")
	}

	if ctx.includeServices {
		// generate services
		if len(ctx.packageData.Services) > 0 {
			generateServices(ctx, w, ctx.packageData.Services)
		}
	}

	return nil
}

// generateHeader writes generated package header into w
func generateHeader(ctx *context, w io.Writer) {
	fmt.Fprintln(w, "// Code generated by GoVPP binapi-generator. DO NOT EDIT.")
	fmt.Fprintf(w, "// source: %s\n", ctx.inputFile)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "/*")
	fmt.Fprintf(w, "Package %s is a generated from VPP binary API module '%s'.\n", ctx.packageName, ctx.moduleName)
	fmt.Fprintln(w)
	fmt.Fprintf(w, " The %s module consists of:\n", ctx.moduleName)
	var printObjNum = func(obj string, num int) {
		if num > 0 {
			if num > 1 {
				if strings.HasSuffix(obj, "s") {

					obj += "es"
				} else {
					obj += "s"
				}
			}
			fmt.Fprintf(w, "\t%3d %s\n", num, obj)
		}
	}

	printObjNum("enum", len(ctx.packageData.Enums))
	printObjNum("alias", len(ctx.packageData.Aliases))
	printObjNum("type", len(ctx.packageData.Types))
	printObjNum("union", len(ctx.packageData.Unions))
	printObjNum("message", len(ctx.packageData.Messages))
	printObjNum("service", len(ctx.packageData.Services))
	fmt.Fprintln(w, "*/")

	fmt.Fprintf(w, "package %s\n", ctx.packageName)
	fmt.Fprintln(w)
}

// generateImports writes generated package imports into w
func generateImports(ctx *context, w io.Writer) {
	fmt.Fprintf(w, "import api \"%s\"\n", govppApiImportPath)
	fmt.Fprintf(w, "import bytes \"%s\"\n", "bytes")
	fmt.Fprintf(w, "import context \"%s\"\n", "context")
	fmt.Fprintf(w, "import strconv \"%s\"\n", "strconv")
	fmt.Fprintf(w, "import struc \"%s\"\n", "github.com/lunixbochs/struc")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "// Reference imports to suppress errors if they are not otherwise used.\n")
	fmt.Fprintf(w, "var _ = api.RegisterMessage\n")
	fmt.Fprintf(w, "var _ = bytes.NewBuffer\n")
	fmt.Fprintf(w, "var _ = context.Background\n")
	fmt.Fprintf(w, "var _ = strconv.Itoa\n")
	fmt.Fprintf(w, "var _ = struc.Pack\n")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "// This is a compile-time assertion to ensure that this generated file")
	fmt.Fprintln(w, "// is compatible with the GoVPP api package it is being compiled against.")
	fmt.Fprintln(w, "// A compilation error at this line likely means your copy of the")
	fmt.Fprintln(w, "// GoVPP api package needs to be updated.")
	fmt.Fprintf(w, "const _ = api.GoVppAPIPackageIsVersion%d // please upgrade the GoVPP api package\n", generatedCodeVersion)
	fmt.Fprintln(w)
}

// generateComment writes generated comment for the object into w
func generateComment(ctx *context, w io.Writer, goName string, vppName string, objKind string) {
	if objKind == "service" {
		fmt.Fprintf(w, "// %s represents VPP binary API services in %s module.\n", goName, ctx.moduleName)
	} else {
		fmt.Fprintf(w, "// %s represents VPP binary API %s '%s':\n", goName, objKind, vppName)
	}

	if !ctx.includeComments {
		return
	}

	var isNotSpace = func(r rune) bool {
		return !unicode.IsSpace(r)
	}

	// print out the source of the generated object
	mapType := false
	objFound := false
	objTitle := fmt.Sprintf(`"%s",`, vppName)
	switch objKind {
	case "alias", "service":
		objTitle = fmt.Sprintf(`"%s": {`, vppName)
		mapType = true
	}

	inputBuff := bytes.NewBuffer(ctx.inputData)
	inputLine := 0

	var trimIndent string
	var indent int
	for {
		line, err := inputBuff.ReadString('\n')
		if err != nil {
			break
		}
		inputLine++

		noSpaceAt := strings.IndexFunc(line, isNotSpace)
		if !objFound {
			indent = strings.Index(line, objTitle)
			if indent == -1 {
				continue
			}
			trimIndent = line[:indent]
			// If no other non-whitespace character then we are at the message header.
			if trimmed := strings.TrimSpace(line); trimmed == objTitle {
				objFound = true
				fmt.Fprintln(w, "//")
			}
		} else if noSpaceAt < indent {
			break // end of the definition in JSON for array types
		} else if objFound && mapType && noSpaceAt <= indent {
			fmt.Fprintf(w, "//\t%s", strings.TrimPrefix(line, trimIndent))
			break // end of the definition in JSON for map types (aliases, services)
		}
		fmt.Fprintf(w, "//\t%s", strings.TrimPrefix(line, trimIndent))
	}

	fmt.Fprintln(w, "//")
}

// generateServices writes generated code for the Services interface into w
func generateServices(ctx *context, w io.Writer, services []Service) {
	const apiName = "Service"
	const implName = "service"

	// generate services comment
	generateComment(ctx, w, apiName, "services", "service")

	// generate interface
	fmt.Fprintf(w, "type %s interface {\n", apiName)
	for _, svc := range services {
		generateServiceMethod(ctx, w, &svc)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)

	// generate client implementation
	fmt.Fprintf(w, "type %s struct {\n", implName)
	fmt.Fprintf(w, "\tch api.Channel\n")
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "func New%[1]s(ch api.Channel) %[1]s {\n", apiName)
	fmt.Fprintf(w, "\treturn &%s{ch}\n", implName)
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)

	for _, svc := range services {
		fmt.Fprintf(w, "func (c *%s) ", implName)
		generateServiceMethod(ctx, w, &svc)
		fmt.Fprintln(w, " {")
		if svc.Stream {
			// TODO: stream responses
			//fmt.Fprintf(w, "\tstream := make(chan *%s)\n", camelCaseName(svc.ReplyType))
			replyTyp := camelCaseName(svc.ReplyType)
			fmt.Fprintf(w, "\tvar dump []*%s\n", replyTyp)
			fmt.Fprintf(w, "\treq := c.ch.SendMultiRequest(in)\n")
			fmt.Fprintf(w, "\tfor {\n")
			fmt.Fprintf(w, "\tm := new(%s)\n", replyTyp)
			fmt.Fprintf(w, "\tstop, err := req.ReceiveReply(m)\n")
			fmt.Fprintf(w, "\tif stop { break }\n")
			fmt.Fprintf(w, "\tif err != nil { return nil, err }\n")
			fmt.Fprintf(w, "\tdump = append(dump, m)\n")
			fmt.Fprintln(w, "}")
			fmt.Fprintf(w, "\treturn dump, nil\n")
		} else if replyTyp := camelCaseName(svc.ReplyType); replyTyp != "" {
			fmt.Fprintf(w, "\tout := new(%s)\n", replyTyp)
			fmt.Fprintf(w, "\terr:= c.ch.SendRequest(in).ReceiveReply(out)\n")
			fmt.Fprintf(w, "\tif err != nil { return nil, err }\n")
			fmt.Fprintf(w, "\treturn out, nil\n")
		} else {
			fmt.Fprintf(w, "\tc.ch.SendRequest(in)\n")
			fmt.Fprintf(w, "\treturn nil\n")
		}
		fmt.Fprintln(w, "}")
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w)
}

// generateServiceMethod writes generated code for the service into w
func generateServiceMethod(ctx *context, w io.Writer, svc *Service) {
	reqTyp := camelCaseName(svc.RequestType)

	// method name is same as parameter type name by default
	method := reqTyp
	if svc.Stream {
		// use Dump as prefix instead of suffix for stream services
		if m := strings.TrimSuffix(method, "Dump"); method != m {
			method = "Dump" + m
		}
	}

	params := fmt.Sprintf("in *%s", reqTyp)
	returns := "error"
	if replyType := camelCaseName(svc.ReplyType); replyType != "" {
		replyTyp := fmt.Sprintf("*%s", replyType)
		if svc.Stream {
			// TODO: stream responses
			//replyTyp = fmt.Sprintf("<-chan %s", replyTyp)
			replyTyp = fmt.Sprintf("[]%s", replyTyp)
		}
		returns = fmt.Sprintf("(%s, error)", replyTyp)
	}

	fmt.Fprintf(w, "\t%s(ctx context.Context, %s) %s", method, params, returns)
}

// generateEnum writes generated code for the enum into w
func generateEnum(ctx *context, w io.Writer, enum *Enum) {
	name := camelCaseName(enum.Name)
	typ := binapiTypes[enum.Type]

	logf(" writing enum %q (%s) with %d entries", enum.Name, name, len(enum.Entries))

	// generate enum comment
	generateComment(ctx, w, name, enum.Name, "enum")

	// generate enum definition
	fmt.Fprintf(w, "type %s %s\n", name, typ)
	fmt.Fprintln(w)

	// generate enum entries
	fmt.Fprintln(w, "const (")
	for _, entry := range enum.Entries {
		fmt.Fprintf(w, "\t%s %s = %v\n", entry.Name, name, entry.Value)
	}
	fmt.Fprintln(w, ")")
	fmt.Fprintln(w)

	// generate enum conversion maps
	fmt.Fprintf(w, "var %s_name = map[%s]string{\n", name, typ)
	for _, entry := range enum.Entries {
		fmt.Fprintf(w, "\t%v: \"%s\",\n", entry.Value, entry.Name)
	}
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "var %s_value = map[string]%s{\n", name, typ)
	for _, entry := range enum.Entries {
		fmt.Fprintf(w, "\t\"%s\": %v,\n", entry.Name, entry.Value)
	}
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "func (x %s) String() string {\n", name)
	fmt.Fprintf(w, "\ts, ok := %s_name[%s(x)]\n", name, typ)
	fmt.Fprintf(w, "\tif ok { return s }\n")
	fmt.Fprintf(w, "\treturn strconv.Itoa(int(x))\n")
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)
}

// generateAlias writes generated code for the alias into w
func generateAlias(ctx *context, w io.Writer, alias *Alias) {
	name := camelCaseName(alias.Name)

	logf(" writing type %q (%s), length: %d", alias.Name, name, alias.Length)

	// generate struct comment
	generateComment(ctx, w, name, alias.Name, "alias")

	// generate struct definition
	fmt.Fprintf(w, "type %s ", name)

	if alias.Length > 0 {
		fmt.Fprintf(w, "[%d]", alias.Length)
	}

	dataType := convertToGoType(ctx, alias.Type)
	fmt.Fprintf(w, "%s\n", dataType)

	fmt.Fprintln(w)
}

// generateUnion writes generated code for the union into w
func generateUnion(ctx *context, w io.Writer, union *Union) {
	name := camelCaseName(union.Name)

	logf(" writing union %q (%s) with %d fields", union.Name, name, len(union.Fields))

	// generate struct comment
	generateComment(ctx, w, name, union.Name, "union")

	// generate struct definition
	fmt.Fprintln(w, "type", name, "struct {")

	// maximum size for union
	maxSize := getUnionSize(ctx, union)

	// generate data field
	fmt.Fprintf(w, "\t%s [%d]byte\n", unionDataField, maxSize)

	// generate end of the struct
	fmt.Fprintln(w, "}")

	// generate name getter
	generateTypeNameGetter(w, name, union.Name)

	// generate CRC getter
	if union.CRC != "" {
		generateCrcGetter(w, name, union.CRC)
	}

	// generate getters for fields
	for _, field := range union.Fields {
		fieldName := camelCaseName(field.Name)
		fieldType := convertToGoType(ctx, field.Type)
		generateUnionGetterSetter(w, name, fieldName, fieldType)
	}

	// generate union methods
	//generateUnionMethods(w, name)

	fmt.Fprintln(w)
}

// generateUnionMethods generates methods that implement struc.Custom
// interface to allow having XXX_uniondata field unexported
// TODO: do more testing when unions are actually used in some messages
/*func generateUnionMethods(w io.Writer, structName string) {
	// generate struc.Custom implementation for union
	fmt.Fprintf(w, `
func (u *%[1]s) Pack(p []byte, opt *struc.Options) (int, error) {
	var b = new(bytes.Buffer)
	if err := struc.PackWithOptions(b, u.union_data, opt); err != nil {
		return 0, err
	}
	copy(p, b.Bytes())
	return b.Len(), nil
}
func (u *%[1]s) Unpack(r io.Reader, length int, opt *struc.Options) error {
	return struc.UnpackWithOptions(r, u.union_data[:], opt)
}
func (u *%[1]s) Size(opt *struc.Options) int {
	return len(u.union_data)
}
func (u *%[1]s) String() string {
	return string(u.union_data[:])
}
`, structName)
}*/

func generateUnionGetterSetter(w io.Writer, structName string, getterField, getterStruct string) {
	fmt.Fprintf(w, `
func %[1]s%[2]s(a %[3]s) (u %[1]s) {
	u.Set%[2]s(a)
	return
}
func (u *%[1]s) Set%[2]s(a %[3]s) {
	var b = new(bytes.Buffer)
	if err := struc.Pack(b, &a); err != nil {
		return
	}
	copy(u.%[4]s[:], b.Bytes())
}
func (u *%[1]s) Get%[2]s() (a %[3]s) {
	var b = bytes.NewReader(u.%[4]s[:])
	struc.Unpack(b, &a)
	return
}
`, structName, getterField, getterStruct, unionDataField)
}

// generateType writes generated code for the type into w
func generateType(ctx *context, w io.Writer, typ *Type) {
	name := camelCaseName(typ.Name)

	logf(" writing type %q (%s) with %d fields", typ.Name, name, len(typ.Fields))

	// generate struct comment
	generateComment(ctx, w, name, typ.Name, "type")

	// generate struct definition
	fmt.Fprintf(w, "type %s struct {\n", name)

	// generate struct fields
	for i, field := range typ.Fields {
		// skip internal fields
		switch strings.ToLower(field.Name) {
		case crcField, msgIdField:
			continue
		}

		generateField(ctx, w, typ.Fields, i)
	}

	// generate end of the struct
	fmt.Fprintln(w, "}")

	// generate name getter
	generateTypeNameGetter(w, name, typ.Name)

	// generate CRC getter
	if typ.CRC != "" {
		generateCrcGetter(w, name, typ.CRC)
	}

	fmt.Fprintln(w)
}

// generateMessage writes generated code for the message into w
func generateMessage(ctx *context, w io.Writer, msg *Message) {
	name := camelCaseName(msg.Name)

	logf(" writing message %q (%s) with %d fields", msg.Name, name, len(msg.Fields))

	// generate struct comment
	generateComment(ctx, w, name, msg.Name, "message")

	// generate struct definition
	fmt.Fprintf(w, "type %s struct {", name)

	msgType := otherMessage
	wasClientIndex := false

	// generate struct fields
	n := 0
	for i, field := range msg.Fields {
		if i == 1 {
			if field.Name == clientIndexField {
				// "client_index" as the second member,
				// this might be an event message or a request
				msgType = eventMessage
				wasClientIndex = true
			} else if field.Name == contextField {
				// reply needs "context" as the second member
				msgType = replyMessage
			}
		} else if i == 2 {
			if wasClientIndex && field.Name == contextField {
				// request needs "client_index" as the second member
				// and "context" as the third member
				msgType = requestMessage
			}
		}

		// skip internal fields
		switch strings.ToLower(field.Name) {
		case crcField, msgIdField:
			continue
		case clientIndexField, contextField:
			if n == 0 {
				continue
			}
		}
		n++
		if n == 1 {
			fmt.Fprintln(w)
		}

		generateField(ctx, w, msg.Fields, i)
	}

	// generate end of the struct
	fmt.Fprintln(w, "}")

	// generate name getter
	generateMessageNameGetter(w, name, msg.Name)

	// generate CRC getter
	generateCrcGetter(w, name, msg.CRC)

	// generate message type getter method
	generateMessageTypeGetter(w, name, msgType)

	fmt.Fprintln(w)
}

// generateField writes generated code for the field into w
func generateField(ctx *context, w io.Writer, fields []Field, i int) {
	field := fields[i]

	fieldName := strings.TrimPrefix(field.Name, "_")
	fieldName = camelCaseName(fieldName)

	// generate length field for strings
	if field.Type == "string" {
		fmt.Fprintf(w, "\tXXX_%sLen uint32 `struc:\"sizeof=%s\"`\n", fieldName, fieldName)
	}

	dataType := convertToGoType(ctx, field.Type)
	fieldType := dataType

	// check if it is array
	if field.Length > 0 || field.SizeFrom != "" {
		if dataType == "uint8" {
			dataType = "byte"
		}
		fieldType = "[]" + dataType
	}
	fmt.Fprintf(w, "\t%s %s", fieldName, fieldType)

	fieldTags := map[string]string{}

	if field.Length > 0 {
		// fixed size array
		fieldTags["struc"] = fmt.Sprintf("[%d]%s", field.Length, dataType)
	} else {
		for _, f := range fields {
			if f.SizeFrom == field.Name {
				// variable sized array
				sizeOfName := camelCaseName(f.Name)
				fieldTags["struc"] = fmt.Sprintf("sizeof=%s", sizeOfName)
			}
		}
	}

	if ctx.includeBinapiNames {
		fieldTags["binapi"] = field.Name
	}
	if field.Meta.Limit > 0 {
		fieldTags["binapi"] = fmt.Sprintf("%s,limit=%d", fieldTags["binapi"], field.Meta.Limit)
	}

	if len(fieldTags) > 0 {
		fmt.Fprintf(w, "\t`")
		var keys []string
		for k := range fieldTags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var n int
		for _, tt := range keys {
			t, ok := fieldTags[tt]
			if !ok {
				continue
			}
			if n > 0 {
				fmt.Fprintf(w, " ")
			}
			n++
			fmt.Fprintf(w, `%s:"%s"`, tt, t)
		}
		fmt.Fprintf(w, "`")
	}

	fmt.Fprintln(w)
}

// generateMessageNameGetter generates getter for original VPP message name into the provider writer
func generateMessageNameGetter(w io.Writer, structName, msgName string) {
	fmt.Fprintf(w, `func (*%s) GetMessageName() string {
	return %q
}
`, structName, msgName)
}

// generateTypeNameGetter generates getter for original VPP type name into the provider writer
func generateTypeNameGetter(w io.Writer, structName, msgName string) {
	fmt.Fprintf(w, `func (*%s) GetTypeName() string {
	return %q
}
`, structName, msgName)
}

// generateCrcGetter generates getter for CRC checksum of the message definition into the provider writer
func generateCrcGetter(w io.Writer, structName, crc string) {
	crc = strings.TrimPrefix(crc, "0x")
	fmt.Fprintf(w, `func (*%s) GetCrcString() string {
	return %q
}
`, structName, crc)
}

// generateMessageTypeGetter generates message factory for the generated message into the provider writer
func generateMessageTypeGetter(w io.Writer, structName string, msgType MessageType) {
	fmt.Fprintln(w, "func (*"+structName+") GetMessageType() api.MessageType {")
	if msgType == requestMessage {
		fmt.Fprintln(w, "\treturn api.RequestMessage")
	} else if msgType == replyMessage {
		fmt.Fprintln(w, "\treturn api.ReplyMessage")
	} else if msgType == eventMessage {
		fmt.Fprintln(w, "\treturn api.EventMessage")
	} else {
		fmt.Fprintln(w, "\treturn api.OtherMessage")
	}
	fmt.Fprintln(w, "}")
}
