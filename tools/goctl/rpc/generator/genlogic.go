package generator

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zeromicro/go-zero/core/collection"
	conf "github.com/zeromicro/go-zero/tools/goctl/config"
	"github.com/zeromicro/go-zero/tools/goctl/rpc/parser"
	"github.com/zeromicro/go-zero/tools/goctl/util"
	"github.com/zeromicro/go-zero/tools/goctl/util/format"
	"github.com/zeromicro/go-zero/tools/goctl/util/pathx"
	"github.com/zeromicro/go-zero/tools/goctl/util/stringx"
)

const logicFunctionTemplate = `{{if .hasComment}}{{.comment}}{{end}}
func (l *{{.logicName}}) {{.method}} ({{if .hasReq}}in {{.request}}{{if .stream}},stream {{.streamBody}}{{end}}{{else}}stream {{.streamBody}}{{end}}) ({{if .hasReply}}{{.response}},{{end}} error) {
	// todo: add your logic here and delete this line
	
	return {{if .hasReply}}&{{.responseType}}{},{{end}} nil
}
`

//go:embed logic.tpl
var logicTemplate string

// GenLogic generates the logic file of the rpc service, which corresponds to the RPC definition items in proto.
func (g *Generator) GenLogic(ctx DirContext, proto parser.Proto, cfg *conf.Config,
	c *ZRpcContext) error {
	if !c.Multiple {
		return g.genLogicInCompatibility(ctx, proto, cfg)
	}

	return g.genLogicGroup(ctx, proto, cfg)
}

func (g *Generator) genLogicInCompatibility(ctx DirContext, proto parser.Proto,
	cfg *conf.Config) error {
	dir := ctx.GetLogic()
	service := proto.Service[0].Service.Name
	for _, rpc := range proto.Service[0].RPC {
		logicName := fmt.Sprintf("%sLogic", stringx.From(rpc.Name).ToCamel())
		logicFilename, err := format.FileNamingFormat(cfg.NamingFormat, rpc.Name+"_logic")
		if err != nil {
			return err
		}
		pK, pV, err := getPrimaryKey(strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1))
		if err != nil {
			pK = "Id"
			pV = "0"
		}

		filename := filepath.Join(dir.Filename, logicFilename+".go")
		functions, err := g.genLogicFunction(service, proto.PbPackage, logicName, rpc, pK, pV)

		if pathx.FileExists(filename) {
			continue
		}

		imports := collection.NewSet()
		imports.AddStr(fmt.Sprintf(`"%v"`, ctx.GetSvc().Package))
		//imports.AddStr(fmt.Sprintf(`"%v"`, ctx.GetPb().Package))
		imports.AddStr(fmt.Sprintf(`proto "proto/%s"`, service))

		imports.AddStr("\"comm/errorm\"")
		if functions.HasSqlc {
			imports.AddStr("\"github.com/zeromicro/go-zero/core/stores/sqlc\"")
		}
		if functions.HasUtil {
			imports.AddStr("\"comm/util\"")
		}
		if functions.HasModel {
			imports.AddStr(fmt.Sprintf(`"%s-service/model"`, service))
		}
		text, err := pathx.LoadTemplate(category, logicTemplateFileFile, logicTemplate)
		if err != nil {
			return err
		}
		err = util.With("logic").GoFmt(true).Parse(text).SaveTo(map[string]interface{}{
			"logicName":   fmt.Sprintf("%sLogic", stringx.From(rpc.Name).ToCamel()),
			"functions":   functions.Fn,
			"packageName": "logic",
			"imports":     strings.Join(imports.KeysStr(), pathx.NL),
		}, filename, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) genLogicGroup(ctx DirContext, proto parser.Proto, cfg *conf.Config) error {
	dir := ctx.GetLogic()

	for _, item := range proto.Service {
		serviceName := item.Name
		for _, rpc := range item.RPC {

			var (
				err           error
				filename      string
				logicName     string
				logicFilename string
				packageName   string
			)

			pK, pV, err := getPrimaryKey(strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1))
			if err != nil {
				pK = "Id"
				pV = "0"
			}

			logicName = fmt.Sprintf("%sLogic", stringx.From(rpc.Name).ToCamel())
			childPkg, err := dir.GetChildPackage(serviceName)
			if err != nil {
				return err
			}

			serviceDir := filepath.Base(childPkg)
			nameJoin := fmt.Sprintf("%s_logic", serviceName)
			packageName = strings.ToLower(stringx.From(nameJoin).ToCamel())
			logicFilename, err = format.FileNamingFormat(cfg.NamingFormat, rpc.Name+"_logic")
			if err != nil {
				return err
			}

			filename = filepath.Join(dir.Filename, serviceDir, logicFilename+".go")
			functions, err := g.genLogicFunction(serviceName, proto.PbPackage, logicName, rpc, pK, pV)
			if err != nil {
				return err
			}

			imports := collection.NewSet()
			imports.AddStr(fmt.Sprintf(`"%v"`, ctx.GetSvc().Package))
			imports.AddStr(fmt.Sprintf(`"%v"`, ctx.GetPb().Package))
			text, err := pathx.LoadTemplate(category, logicTemplateFileFile, logicTemplate)
			if err != nil {
				return err
			}

			if err = util.With("logic").GoFmt(true).Parse(text).SaveTo(map[string]interface{}{
				"logicName":   logicName,
				"functions":   functions.Fn,
				"packageName": packageName,
				"imports":     strings.Join(imports.KeysStr(), pathx.NL),
			}, filename, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// func (g *Generator) genLogicFunction(serviceName, goPackage, logicName string,
// 	rpc *parser.RPC) (string,
// 	error) {
type genLogic struct {
	HasSqlc  bool
	HasUtil  bool
	HasModel bool

	Fn string
}

func (g *Generator) genLogicFunction(serviceName, goPackage string, logicName string, rpc *parser.RPC, pK, pV string) (genLogic, error) {
	functions := make([]string, 0)
	gen := genLogic{}
	text, err := pathx.LoadTemplate(category, logicFuncTemplateFileFile, logicFunctionTemplate)
	if err != nil {
		return gen, err
	}
	modelName := ""

	// load curd template
	switch parser.CamelCase(rpc.Name) {
	case fmt.Sprintf("Create%s", parser.CamelCase(rpc.RequestType)):
		text = CreateLogic
		modelName = parser.CamelCase(rpc.RequestType)
		gen.HasSqlc = true
		gen.HasModel = true
	case fmt.Sprintf("Delete%s", parser.CamelCase(rpc.RequestType)):
		text = DeleteLogic
		modelName = parser.CamelCase(rpc.RequestType)
		gen.HasModel = true
	case fmt.Sprintf("Query%sDetail", strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1)):
		text = QueryDetailLogic
		modelName = strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1)
		gen.HasModel = true
	case fmt.Sprintf("Query%sList", strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1)):
		text = QueryLogic
		modelName = strings.Replace(parser.CamelCase(rpc.RequestType), "Filter", "", 1)
		gen.HasUtil = true
		gen.HasModel = true

	case fmt.Sprintf("Update%s", parser.CamelCase(rpc.RequestType)):
		text = UpdateLogic
		gen.HasSqlc = true
		gen.HasUtil = true
		modelName = parser.CamelCase(rpc.RequestType)
		gen.HasModel = true
	}

	comment := parser.GetComment(rpc.Doc())
	// fmt.Printf("method[%s] comment[%s]", parser.CamelCase(rpc.Name), comment)
	streamServer := fmt.Sprintf("%s.%s_%s%s", goPackage, parser.CamelCase(serviceName),
		parser.CamelCase(rpc.Name), "Server")
	buffer, err := util.With("fun").Parse(text).Execute(map[string]interface{}{
		"pK":                  pK,
		"pV":                  pV,
		"logicName":           logicName,
		"method":              parser.CamelCase(rpc.Name),
		"hasReq":              !rpc.StreamsRequest,
		"request":             fmt.Sprintf("*%s.%s", "proto", parser.CamelCase(rpc.RequestType)),
		"hasReply":            !rpc.StreamsRequest && !rpc.StreamsReturns,
		"response":            fmt.Sprintf("*%s.%s", "proto", parser.CamelCase(rpc.ReturnsType)),
		"responseType":        fmt.Sprintf("%s.%s", "proto", parser.CamelCase(rpc.ReturnsType)),
		"stream":              rpc.StreamsRequest || rpc.StreamsReturns,
		"streamBody":          streamServer,
		"hasComment":          len(comment) > 0,
		"comment":             comment,
		"modelName":           modelName,
		"modelNameFirstLower": FirstLower(modelName),
	})
	if err != nil {
		return gen, err
	}

	functions = append(functions, buffer.String())
	gen.Fn = strings.Join(functions, pathx.NL)
	return gen, nil
}

// FirstLower 字符串首字母小写
func FirstLower(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToLower(s[:1]) + s[1:]
}

var (
	dbOnce       sync.Once
	VarStringURL string
	DB           *sql.DB
)

// 获取临时文件中的主键
func getPrimaryKey(modelName string) (camels string, mark string, err error) {
	defer func() {
		if msg := recover(); err != nil {
			err = fmt.Errorf("getPrimaryKey recover%v", msg)
		}
	}()

	type PKey struct {
		ColumnName string
		DataType   string
	}

	var (
		PK PKey
	)

	tableName := snakeString(modelName)

	row := GetDb().QueryRow("select c.COLUMN_NAME, c.DATA_TYPE from INFORMATION_SCHEMA.COLUMNS as c where table_name= ? AND COLUMN_KEY='PRI'", tableName)

	if err = row.Scan(&PK.ColumnName, &PK.DataType); err != nil {
		return "", "", err
	}

	switch PK.DataType {
	case "char", "varchar", "text", "longtext", "mediumtext", "tinytext":
		mark = `""`
	default:
		mark = "0"
	}

	return camel(PK.ColumnName), mark, nil
}

func camel(name string) string {
	name = strings.Replace(name, "_", " ", -1)
	name = strings.Title(name)
	return strings.Replace(name, " ", "", -1)
}

func snakeString(s string) string {
	data := make([]byte, 0, len(s)*2)
	j := false
	num := len(s)
	for i := 0; i < num; i++ {
		d := s[i]

		if i > 0 && d >= 'A' && d <= 'Z' && j {
			data = append(data, '_')
		}
		if d != '_' {
			j = true
		}
		data = append(data, d)
	}

	return strings.ToLower(string(data[:]))
}

func GetDb() *sql.DB {
	var err error

	dbOnce.Do(func() {
		url := VarStringURL
		DB, err = sql.Open("mysql", url)
		if err != nil {
			// log.Fatal(err)
			fmt.Println("getDb err !")
		}
	})

	return DB
}
