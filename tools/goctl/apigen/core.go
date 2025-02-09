package apigen

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/serenize/snaker"
	"github.com/zeromicro/go-zero/tools/goctl/util/stringx"
)

const (
	synatx = "v1"

	indent = "  "
)

func GenerateSchema(db *sql.DB, table string, ignoreTables []string, serviceName string, dir string) (*Schema, error) {

	var err error

	_, err = os.Stat(dir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			fmt.Printf("创建文件错误:%v", err)
			panic(err)
		}
	}

	s := &Schema{
		Dir: dir,
	}

	dbs, err := dbSchema(db)
	if nil != err {
		return nil, err
	}

	s.Syntax = synatx
	s.ServiceName = serviceName
	cols, err := dbColumns(db, dbs, table)
	if nil != err {
		return nil, err
	}

	if len(cols) == 0 {
		return nil, errors.New("no columns to genertor!!!")
	}
	err = typesFromColumns(s, cols, ignoreTables)
	if nil != err {
		return nil, err
	}

	sort.Sort(s.Imports)
	sort.Sort(s.Messages)
	sort.Sort(s.Enums)

	return s, nil
}

// 指定proto文件生成xxxParam.api中type
func GenerateProtoType(s *Schema, serviceName string, protoFile, dir string) (*Schema, error) {
	var err error

	_, err = os.Stat(dir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			fmt.Printf("创建文件错误:%v", err)
			panic(err)
		}
	}

	if s == nil {
		s = &Schema{
			Dir: dir,
		}
	}

	s.Syntax = synatx
	s.ServiceName = serviceName

	if err = typesFromProto(s, protoFile, serviceName); err != nil {
		fmt.Println(err)
	}

	sort.Sort(s.Imports)
	sort.Sort(s.Messages)
	sort.Sort(s.Enums)

	return s, nil
}

func typesFromProto(s *Schema, file, serviceName string) error {
	if file == "" {
		file = "./rpc/proto/" + serviceName + ".proto"
	}

	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := bufio.NewReader(f)
	var strs []string

Loop:
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return errors.New("Read file error!")
			}
		}

		if strings.Contains(line, "Api Struct Gen") {
			for {
				line, _ := buf.ReadString('\n')
				if strings.Contains(line, "Struct Gen End") {
					break Loop
				}

				str := strings.Replace(line, "//", "", -1)
				str = strings.TrimSpace(str)
				strs = append(strs, str)
			}
		}

	}

	bufNew := new(bytes.Buffer)

	if _, err = buf.WriteTo(bufNew); err != nil {
		return err
	}

	tmpStr := bufNew.String()

	for _, v := range strs {

		reg := fmt.Sprintf("message %s%s", v, `[\s]*{[^}]+}`)
		re := regexp.MustCompile(reg)
		oldSubStrings := re.FindStringSubmatch(tmpStr)

		if oldSubStrings[0] == "" {
			continue
		}

		split := strings.Split(oldSubStrings[0], "\n")

		message := &Message{
			Name:   snaker.SnakeToCamel(v),
			Fields: make([]MessageField, 0, len(split)-1),
		}

		for i := 1; i < len(split)-1; i++ {
			var n = 2

			i2 := strings.Split(split[i], " ")
			if len(i2) == 8 && i2[n] == "repeated" {
				n += 1
				i2[n] = "[]*" + i2[n]
			}

			if len(i2) < 7 || strings.Contains(i2[n], "//") {
				continue
			}

			field := NewMessageField(i2[n], i2[n+1], strings.Replace(i2[n+4], "//", "", -1), snaker.CamelToSnake(i2[n+1]))

			message.AppendField(field)
		}

		s.CusMessages = append(s.CusMessages, message)
	}

	return nil
}

// typesFromColumns creates the appropriate schema properties from a collection of column types.
func typesFromColumns(s *Schema, cols []Column, ignoreTables []string) error {
	messageMap := map[string]*Message{}
	ignoreMap := map[string]bool{}

	if len(ignoreTables) != 0 {
		for _, ig := range ignoreTables {
			ignoreMap[ig] = true
		}
	}

	for _, c := range cols {
		if _, ok := ignoreMap[c.TableName]; ok {
			continue
		}

		messageName := snaker.SnakeToCamel(c.TableName)
		// messageName = inflect.Singularize(messageName)

		msg, ok := messageMap[messageName]
		if !ok {
			messageMap[messageName] = &Message{Name: messageName, Comment: c.TableComment}
			msg = messageMap[messageName]
		}

		err := parseColumn(s, msg, c)
		if nil != err {
			return err
		}
	}

	for _, v := range messageMap {
		s.Messages = append(s.Messages, v)
	}

	return nil
}

func dbSchema(db *sql.DB) (string, error) {
	var schema string

	err := db.QueryRow("SELECT SCHEMA()").Scan(&schema)

	return schema, err
}

func dbColumns(db *sql.DB, schema, table string) ([]Column, error) {

	tableArr := strings.Split(table, ",")
	if len(tableArr) == 0 {
		return nil, errors.New("no table to genertor")
	}
	q := "SELECT c.TABLE_NAME, c.COLUMN_NAME, c.IS_NULLABLE, c.DATA_TYPE, " +
		"c.CHARACTER_MAXIMUM_LENGTH, c.NUMERIC_PRECISION, c.NUMERIC_SCALE, c.COLUMN_TYPE ,c.COLUMN_COMMENT,t.TABLE_COMMENT " +
		"FROM INFORMATION_SCHEMA.COLUMNS as c  LEFT JOIN  INFORMATION_SCHEMA.TABLES as t  on c.TABLE_NAME = t.TABLE_NAME and  c.TABLE_SCHEMA = t.TABLE_SCHEMA" +
		" WHERE c.TABLE_SCHEMA = ?"

	if table != "" && table != "*" {
		q += " AND c.TABLE_NAME IN('" + strings.TrimRight(strings.Join(tableArr, "' ,'"), ",") + "')"
	}

	q += " ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION"

	rows, err := db.Query(q, schema)
	defer rows.Close()
	if nil != err {
		return nil, err
	}

	cols := []Column{}

	for rows.Next() {
		cs := Column{}
		err := rows.Scan(&cs.TableName, &cs.ColumnName, &cs.IsNullable, &cs.DataType,
			&cs.CharacterMaximumLength, &cs.NumericPrecision, &cs.NumericScale, &cs.ColumnType, &cs.ColumnComment, &cs.TableComment)
		if err != nil {
			log.Fatal(err)
		}

		if cs.TableComment == "" {
			cs.TableComment = stringx.From(cs.TableName).ToCamelWithStartLower()
		}
		//这里过滤掉不需要生成的字段
		if !isInSlice([]string{"create_time", "update_time"}, cs.ColumnName) {
			cols = append(cols, cs)
		}

	}
	if err := rows.Err(); nil != err {
		return nil, err
	}

	return cols, nil
}

// Schema is a representation of a protobuf schema.
type Schema struct {
	Syntax             string
	ServiceName        string
	Dir                string
	Imports            sort.StringSlice
	Messages           MessageCollection
	CusMessages        MessageCollection
	Enums              EnumCollection
	GenerateCurdMethod []string
}

// MessageCollection represents a sortable collection of messages.
type MessageCollection []*Message

func (mc MessageCollection) Len() int {
	return len(mc)
}

func (mc MessageCollection) Less(i, j int) bool {
	return mc[i].Name < mc[j].Name
}

func (mc MessageCollection) Swap(i, j int) {
	mc[i], mc[j] = mc[j], mc[i]
}

// EnumCollection represents a sortable collection of enums.
type EnumCollection []*Enum

func (ec EnumCollection) Len() int {
	return len(ec)
}

func (ec EnumCollection) Less(i, j int) bool {
	return ec[i].Name < ec[j].Name
}

func (ec EnumCollection) Swap(i, j int) {
	ec[i], ec[j] = ec[j], ec[i]
}

// AppendImport adds an import to a schema if the specific import does not already exist in the schema.
func (s *Schema) AppendImport(imports string) {
	shouldAdd := true
	for _, si := range s.Imports {
		if si == imports {
			shouldAdd = false
			break
		}
	}

	if shouldAdd {
		s.Imports = append(s.Imports, imports)
	}

}

func (s *Schema) String() string {
	var err error
	paramFile := fmt.Sprintf("%s%sParam.api", s.Dir, s.ServiceName)

	_, err = os.Stat(paramFile)
	if os.IsNotExist(err) {
		s.CreateParamString(paramFile)
	} else {
		s.UpdateParamString(paramFile)
	}

	_, err = os.Stat(fmt.Sprintf("%s%s.api", s.Dir, s.ServiceName))
	//如果返回的错误类型使用os.isNotExist()判断为true，说明文件或者文件夹不存在
	if os.IsNotExist(err) {
		return s.CreateString()
	}

	return s.UpdateString()
}

func (s *Schema) CreateParamString(fileName string) string {
	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("syntax = \"%s\"\n", s.Syntax))
	buf.WriteString("\n")
	buf.WriteString("// Already Exist Table:\n")
	for _, m := range s.Messages {
		buf.WriteString("// " + m.Name)
		buf.WriteString("\n")
	}
	buf.WriteString("// Exist Table End\n")
	buf.WriteString("\n")

	buf.WriteString("// Proto Customize Type:\n")
	for _, m := range s.CusMessages {
		buf.WriteString("// " + m.Name)
		buf.WriteString("\n")
	}
	buf.WriteString("// Customize Type End\n")
	buf.WriteString("\n")

	buf.WriteString("// Type Record Start\n")

	for _, m := range s.Messages {
		buf.WriteString("//--------------------------------" + m.Comment + "--------------------------------")
		buf.WriteString("\n")
		buf.WriteString("type (\n")
		// 创建
		m.GenApiDefault(buf)
		buf.WriteString("\n")
		m.GenApiDefaultResp(buf)
		buf.WriteString("\n")

		//更新
		m.GenApiUpdateReq(buf)
		buf.WriteString("\n")
		m.GenApiUpdateResp(buf)
		buf.WriteString("\n")

		//查询
		m.GenApiQueryListReq(buf)
		buf.WriteString("\n")
		m.GenApiQueryListResp(buf)

	}

	buf.WriteString(")")
	buf.WriteString("\n\n")

	for _, m := range s.CusMessages {

		// 创建
		buf.WriteString("//--------------------------------" + "customize_proto" + m.Name + "--------------------------------")
		buf.WriteString("\n")
		buf.WriteString("type (\n")

		m.GenApiDefault(buf)

		buf.WriteString(")")
		buf.WriteString("\n\n")
	}

	buf.WriteString("// Type Record End\n")
	err := ioutil.WriteFile(fileName, buf.Bytes(), 0666)
	if err != nil {
		fmt.Printf("生成paramFile文件失败:%v", err.Error())
		return ""
	}

	return "paramFile Done"
}
func (s *Schema) UpdateParamString(fileName string) string {
	bufNew := new(bytes.Buffer)
	file, err := os.OpenFile(fileName, os.O_RDWR, 0666)
	if err != nil {
		return fmt.Sprintf("Open file error!%v", err)
	}

	stat, err := file.Stat()
	if err != nil {
		panic(err)
	}

	var _ = stat.Size()
	endLine := ""
	buf := bufio.NewReader(file)
	//写已存在表名
	for {
		line, err := buf.ReadString('\n')
		bufNew.WriteString(line)
		if strings.Contains(line, "Already Exist Table") {
			break
		}
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	var existTableName []string

	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Exist Table End") {
			endLine = line
			break
		}
		existTableName = append(existTableName, line[3:])
		bufNew.WriteString(line)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	var newTableNames []string

	for _, m := range s.Messages {
		if !isInSlice(existTableName, m.Name) {
			newTableNames = append(newTableNames, m.Name)
			bufNew.WriteString("// " + m.Name + "\n")
		}
	}
	bufNew.WriteString(endLine)

	//写已存在结构体
	for {
		line, err := buf.ReadString('\n')
		bufNew.WriteString(line)
		if strings.Contains(line, "Proto Customize Type") {
			break
		}
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	var existFieldName []string

	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Customize Type End") {
			endLine = line
			break
		}
		existFieldName = append(existFieldName, line[3:])
		bufNew.WriteString(line)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	var newFieldNames []string

	for _, m := range s.CusMessages {
		if !isInSlice(existFieldName, m.Name) {
			newFieldNames = append(newFieldNames, m.Name)
			bufNew.WriteString("// " + m.Name + "\n")
		}
	}
	bufNew.WriteString(endLine)

	// 写Messages
	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Type Record End") {
			endLine = line
			break
		}
		bufNew.WriteString(line)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	for _, m := range s.Messages {
		if !isInSlice(existTableName, m.Name) {
			bufNew.WriteString("//--------------------------------" + m.Comment + "--------------------------------")
			bufNew.WriteString("\n")
			bufNew.WriteString("type (\n")

			// 创建
			m.GenApiDefault(bufNew)
			bufNew.WriteString("\n")
			m.GenApiDefaultResp(bufNew)
			bufNew.WriteString("\n")

			//更新
			m.GenApiUpdateReq(bufNew)
			bufNew.WriteString("\n")
			m.GenApiUpdateResp(bufNew)
			bufNew.WriteString("\n")

			//查询
			m.GenApiQueryListReq(bufNew)
			bufNew.WriteString("\n")
			m.GenApiQueryListResp(bufNew)

			bufNew.WriteString(")")
			bufNew.WriteString("\n\n")
		}
	}

	for _, m := range s.CusMessages {
		if !isInSlice(existFieldName, m.Name) {
			bufNew.WriteString("//--------------------------------" + "customize_proto" + m.Name + "--------------------------------")
			bufNew.WriteString("\n")
			bufNew.WriteString("type (\n")

			// 创建
			m.GenApiDefault(bufNew)
			bufNew.WriteString("\n")

			bufNew.WriteString(")")
			bufNew.WriteString("\n\n")

		}
	}

	bufNew.WriteString("// Type Record End\n")
	err = ioutil.WriteFile(fileName, bufNew.Bytes(), 0666)

	return "paramFile DONE"
}

// String returns a string representation of a Schema.
func (s *Schema) CreateString() string {
	fileName := fmt.Sprintf("%s%s.api", s.Dir, s.ServiceName)

	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("syntax = \"%s\"\n", s.Syntax))
	buf.WriteString("\n")
	buf.WriteString("import (\n")

	buf.WriteString(fmt.Sprintf("\t\"%s\" \n", s.ServiceName+"Param.api"))
	buf.WriteString(")")
	buf.WriteString("\n")
	buf.WriteString("// Already Exist Table:\n")
	for _, m := range s.Messages {
		buf.WriteString("// " + m.Name)
		buf.WriteString("\n")
	}
	buf.WriteString("// Exist Table End\n")
	buf.WriteString("\n")
	if len(s.Enums) > 0 {
		buf.WriteString("// Enums Record Start\n")
		for _, e := range s.Enums {
			buf.WriteString(fmt.Sprintf("%s\n", e))
		}
		buf.WriteString("// Enums Record End\n")
	}

	buf.WriteString("\n")
	buf.WriteString("// ------------------------------------ \n")
	buf.WriteString("// api Func\n")
	buf.WriteString("// ------------------------------------ \n\n")

	funcTpl := "service " + s.ServiceName + "{\n"
	for _, m := range s.Messages {
		funcTpl += "\t//-----------------------" + m.Comment + "----------------------- \n"
		firstUpperName := FirstUpper(m.Name)
		if len(s.GenerateCurdMethod) == 1 && strings.TrimSpace(s.GenerateCurdMethod[0]) == "" {
			funcTpl += "\t@doc  \"" + m.Name + "列表查找[auto]\"\n"
			funcTpl += "\t@handler  query" + m.Name + "List\n"
			funcTpl += "\tget /" + firstUpperName + "/query" + " (Query" + firstUpperName + "Req) returns (Query" + m.Name + "Resp); \n\n"

			funcTpl += "\t@doc  \"" + m.Name + "查找[auto]\"\n"
			funcTpl += "\t@handler  query" + m.Name + "\n"
			funcTpl += "\tget /" + firstUpperName + " (Query" + firstUpperName + "Req) returns (" + firstUpperName + "); \n\n"
		} else {
			if isInSlice(s.GenerateCurdMethod, INSERT) {
				funcTpl += "\t@doc  \"" + m.Name + "创建[auto]\"\n"
				funcTpl += "\t@handler  create" + m.Name + "\n"
				funcTpl += "\tpost /" + firstUpperName + "/create" + " (" + m.Name + ") returns (Create" + firstUpperName + "Resp); \n\n"
			}
			if isInSlice(s.GenerateCurdMethod, UPDATE) {
				funcTpl += "\t@doc  \"" + m.Name + "更新[auto]\"\n"
				funcTpl += "\t@handler  update" + m.Name + "\n"
				funcTpl += "\tpost /" + firstUpperName + "/update" + " (Update" + m.Name + "Req) returns (Update" + firstUpperName + "Resp); \n\n"
			}
			if isInSlice(s.GenerateCurdMethod, QUERY) {
				funcTpl += "\t@doc  \"" + m.Name + "列表查找[auto]\"\n"
				funcTpl += "\t@handler  query" + m.Name + "List\n"
				funcTpl += "\tget /" + firstUpperName + "/query" + " (Query" + firstUpperName + "Req) returns (Query" + m.Name + "Resp); \n\n"

				funcTpl += "\t@doc  \"" + m.Name + "查找[auto]\"\n"
				funcTpl += "\t@handler  query" + m.Name + "\n"
				funcTpl += "\tget /" + firstUpperName + " (Query" + firstUpperName + "Req) returns (" + firstUpperName + "); \n\n"
			}
		}

	}
	funcTpl = funcTpl + "\t // Service Record End\n"
	funcTpl = funcTpl + "}"
	buf.WriteString(funcTpl)
	err := ioutil.WriteFile(fileName, buf.Bytes(), 0666)
	if err != nil {
		return ""
	}
	return "DONE"
}

// String returns a string representation of a Schema.
func (s *Schema) UpdateString() string {
	bufNew := new(bytes.Buffer)
	fileName := fmt.Sprintf("%s%s.api", s.Dir, s.ServiceName)
	file, err := os.OpenFile(fileName, os.O_RDWR, 0666)
	if err != nil {
		return fmt.Sprintf("Open file error!%v", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		panic(err)
	}

	var _ = stat.Size()
	endLine := ""
	buf := bufio.NewReader(file)

	//写已存在表名
	for {
		line, err := buf.ReadString('\n')
		bufNew.WriteString(line)
		if strings.Contains(line, "Already Exist Table") {
			break
		}
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}
	var existTableName []string

	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Exist Table End") {
			endLine = line
			break
		}
		existTableName = append(existTableName, line[3:])
		bufNew.WriteString(line)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}
	var newTableNames []string
	for _, m := range s.Messages {
		if !isInSlice(existTableName, m.Name) {
			newTableNames = append(newTableNames, m.Name)
			bufNew.WriteString("// " + m.Name + "\n")
		}
	}
	bufNew.WriteString(endLine)
	if len(s.Messages) > 0 {
		bufNew.WriteString(endLine)
	}

	// 写enum
	var existEnumText []string
	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Enums Record End") {
			endLine = line
			break
		}

		if strings.Contains(line, "api Func") {
			bufNew.WriteString(line)
			break
		}

		bufNew.WriteString(line)
		reg := regexp.MustCompile(`^enum [A-Za-z0-9]*`)
		enumText := reg.FindAllString(line, -1)
		if enumText != nil {
			existEnumText = append(existEnumText, enumText[0][5:])
		}
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	if len(s.Enums) > 0 {
		for _, e := range s.Enums {
			if !isInSlice(existEnumText, e.Name) {
				bufNew.WriteString(fmt.Sprintf("%s\n", e))
			}
		}
		bufNew.WriteString(endLine)
	}

	// 写api接口名
	for {
		line, err := buf.ReadString('\n')
		if strings.Contains(line, "Service Record End") {
			break
		}
		bufNew.WriteString(line)
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "Read file error!"
			}
		}
	}

	funcTpl := ""
	for _, m := range s.Messages {
		if !isInSlice(existTableName, m.Name) {
			funcTpl += "\t//-----------------------" + m.Comment + "----------------------- \n"

			firstUpperName := FirstUpper(m.Name)
			if len(s.GenerateCurdMethod) == 1 && strings.TrimSpace(s.GenerateCurdMethod[0]) == "" {
				funcTpl += "\t@doc  \"" + m.Name + "列表查找[auto]\"\n"
				funcTpl += "\t@handler  query" + m.Name + "List\n"
				funcTpl += "\tget /" + firstUpperName + "/query" + " (Query" + firstUpperName + "Req) returns (Query" + m.Name + "Resp); \n\n"

				funcTpl += "\t@doc  \"" + m.Name + "查找[auto]\"\n"
				funcTpl += "\t@handler  query" + m.Name + "\n"
				funcTpl += "\tget /" + firstUpperName + " (Query" + firstUpperName + "Req) returns (" + firstUpperName + "); \n\n"
			} else {
				if isInSlice(s.GenerateCurdMethod, INSERT) {
					funcTpl += "\t@doc  \"" + m.Name + "创建[auto]\"\n"
					funcTpl += "\t@handler  create" + m.Name + "\n"
					funcTpl += "\tpost /" + firstUpperName + "/create" + " (" + m.Name + ") returns (Create" + firstUpperName + "Resp); \n\n"
				}
				if isInSlice(s.GenerateCurdMethod, UPDATE) {
					funcTpl += "\t@doc  \"" + m.Name + "更新[auto]\"\n"
					funcTpl += "\t@handler  update" + m.Name + "\n"
					funcTpl += "\tpost /" + firstUpperName + "/update" + " (Update" + m.Name + "Req) returns (Update" + firstUpperName + "Resp); \n\n"
				}
				if isInSlice(s.GenerateCurdMethod, QUERY) {
					funcTpl += "\t@doc  \"" + m.Name + "列表查找[auto]\"\n"
					funcTpl += "\t@handler  query" + m.Name + "List\n"
					funcTpl += "\tget /" + firstUpperName + "/query" + " (Query" + firstUpperName + "Req) returns (Query" + m.Name + "Resp); \n\n"

					funcTpl += "\t@doc  \"" + m.Name + "查找[auto]\"\n"
					funcTpl += "\t@handler  query" + m.Name + "\n"
					funcTpl += "\tget /" + firstUpperName + " (Query" + firstUpperName + "Req) returns (" + firstUpperName + "); \n\n"
				}
			}
		}
	}
	funcTpl = funcTpl + "\t // Service Record End\n"
	funcTpl = funcTpl + "}"

	bufNew.WriteString(funcTpl)

	err = ioutil.WriteFile(fileName, bufNew.Bytes(), 0666) //写入文件(字节数组)
	if err != nil {
		panic(err)
	}
	return "Done"
}

// Enum represents a protocol buffer enumerated type.
type Enum struct {
	Name    string
	Comment string
	Fields  []EnumField
}

// String returns a string representation of an Enum.
func (e *Enum) String() string {
	buf := new(bytes.Buffer)

	buf.WriteString(fmt.Sprintf("// %s \n", e.Comment))
	buf.WriteString(fmt.Sprintf("enum %s {\n", e.Name))

	for _, f := range e.Fields {
		buf.WriteString(fmt.Sprintf("%s%s;\n", indent, f))
	}

	buf.WriteString("}\n")

	return buf.String()
}

// AppendField appends an EnumField to an Enum.
func (e *Enum) AppendField(ef EnumField) error {
	for _, f := range e.Fields {
		if f.Tag() == ef.Tag() {
			return fmt.Errorf("tag `%d` is already in use by field `%s`", ef.Tag(), f.Name())
		}
	}

	e.Fields = append(e.Fields, ef)

	return nil
}

// EnumField represents a field in an enumerated type.
type EnumField struct {
	name string
	tag  int
}

// NewEnumField constructs an EnumField type.
func NewEnumField(name string, tag int) EnumField {
	name = strings.ToUpper(name)

	re := regexp.MustCompile(`([^\w]+)`)
	name = re.ReplaceAllString(name, "_")

	return EnumField{name, tag}
}

// String returns a string representation of an Enum.
func (ef EnumField) String() string {
	return fmt.Sprintf("%s = %d", ef.name, ef.tag)
}

// Name returns the name of the enum field.
func (ef EnumField) Name() string {
	return ef.name
}

// Tag returns the identifier tag of the enum field.
func (ef EnumField) Tag() int {
	return ef.tag
}

// newEnumFromStrings creates an enum from a name and a slice of strings that represent the names of each field.
func newEnumFromStrings(name, comment string, ss []string) (*Enum, error) {
	enum := &Enum{}
	enum.Name = name
	enum.Comment = comment

	for i, s := range ss {
		err := enum.AppendField(NewEnumField(s, i))
		if nil != err {
			return nil, err
		}
	}

	return enum, nil
}

// Message represents a protocol buffer message.
type Message struct {
	Name    string
	Comment string
	Fields  []MessageField
}

// gen default message
func (m Message) GenApiDefault(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields
	curFields := []MessageField{}
	for _, field := range m.Fields {
		if isInSlice([]string{"version", "del_state", "delete_time"}, field.Name) {
			continue
		}
		field.Name = stringx.From(field.Name).ToCamelWithStartLower()
		if field.Comment == "" {
			field.Comment = field.Name
		}
		curFields = append(curFields, field)
	}
	m.Fields = curFields
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields
}

// 先固定写为id
func (m Message) GenApiDefaultResp(buf *bytes.Buffer) {
	mOrginName := FirstUpper(m.Name)
	buf.WriteString(fmt.Sprintf("%sCreate%sResp {\n", indent, mOrginName))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   \n", indent, indent, "Id", "int64", "id"))
	buf.WriteString(fmt.Sprintf("%s}\n", indent))
}

func (m Message) GenApiUpdateReq(buf *bytes.Buffer) {
	mOrginName := FirstUpper(m.Name)
	buf.WriteString(fmt.Sprintf("%sUpdate%sReq {\n", indent, mOrginName))
	for _, f := range m.Fields {
		buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   //%s\n", indent, indent, FirstUpper(stringx.From(f.Name).ToCamelWithStartLower()), f.Typ, f.ColumnName, f.Comment))
	}
	buf.WriteString(fmt.Sprintf("%s}\n", indent))
}

func (m Message) GenApiUpdateResp(buf *bytes.Buffer) {
	mOrginName := FirstUpper(m.Name)
	buf.WriteString(fmt.Sprintf("%sUpdate%sResp {\n", indent, mOrginName))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   \n", indent, indent, "Id", "int64", "id"))
	buf.WriteString(fmt.Sprintf("%s}\n", indent))
}

// 先固定三个参数
func (m Message) GenApiQueryListReq(buf *bytes.Buffer) {
	mOrginName := FirstUpper(m.Name)
	buf.WriteString(fmt.Sprintf("%sQuery%sReq {\n", indent, mOrginName))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `form:\"%s,optional\"`   \n", indent, indent, "Id", "int64", "id"))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `form:\"%s,optional\"`   \n", indent, indent, "PageNo", "int64", "page_no"))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `form:\"%s,optional\"`   \n", indent, indent, "PageSize", "int64", "page_size"))
	buf.WriteString(fmt.Sprintf("%s}\n", indent))
}

func (m Message) GenApiQueryListResp(buf *bytes.Buffer) {
	mOrginName := FirstUpper(m.Name)
	buf.WriteString(fmt.Sprintf("%sQuery%sResp {\n", indent, mOrginName))
	buf.WriteString(fmt.Sprintf("%s%s%s   []%s  `json:\"%s\"`   \n", indent, indent, m.Name+"List", mOrginName, fmt.Sprintf("%s_list", FirstToLower(m.Name))))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   \n", indent, indent, "CurrPage", "int64", "curr_page"))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   \n", indent, indent, "TotalPage", "int64", "total_page"))
	buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`   \n", indent, indent, "TotalCount", "int64", "total_count"))
	buf.WriteString(fmt.Sprintf("%s}\n", indent))
}

// String returns a string representation of a Message.
func (m Message) String() string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("%s%s {\n", indent, m.Name))
	for _, f := range m.Fields {
		buf.WriteString(fmt.Sprintf("%s%s%s   %s  `json:\"%s\"`  ; //%s\n", indent, indent, FirstUpper(f.Name), f.Typ, f.ColumnName, f.Comment))
	}
	buf.WriteString(fmt.Sprintf("%s}\n", indent))

	return buf.String()
}

// MessageField represents the field of a message.
type MessageField struct {
	Typ        string
	Name       string
	Comment    string
	ColumnName string
}

// NewMessageField creates a new message field.
func NewMessageField(typ, name, comment, columnName string) MessageField {
	return MessageField{typ, name, comment, columnName}
}

func (m *Message) AppendField(mf MessageField) error {
	m.Fields = append(m.Fields, mf)
	return nil
}

// String returns a string representation of a message field.
func (f MessageField) String() string {
	return fmt.Sprintf("%s %s  `json:\"%s\"`", f.Name, f.Typ, f.ColumnName)
}

// Column represents a database column.
type Column struct {
	TableName              string
	TableComment           string
	ColumnName             string
	IsNullable             string
	DataType               string
	CharacterMaximumLength sql.NullInt64
	NumericPrecision       sql.NullInt64
	NumericScale           sql.NullInt64
	ColumnType             string
	ColumnComment          string
}

// Table represents a database table.
type Table struct {
	TableName  string
	ColumnName string
}

// parseColumn parses a column and inserts the relevant fields in the Message. If an enumerated type is encountered, an Enum will
// be added to the Schema. Returns an error if an incompatible protobuf data type cannot be found for the database column type.
func parseColumn(s *Schema, msg *Message, col Column) error {
	typ := strings.ToLower(col.DataType)
	var fieldType string

	switch typ {
	case "char", "varchar", "text", "longtext", "mediumtext", "tinytext":
		fieldType = "string"
	case "enum", "set":
		// Parse c.ColumnType to get the enum list
		enumList := regexp.MustCompile(`[enum|set]\((.+?)\)`).FindStringSubmatch(col.ColumnType)
		enums := strings.FieldsFunc(enumList[1], func(c rune) bool {
			cs := string(c)
			return "," == cs || "'" == cs
		})

		// enumName := inflect.Singularize(snaker.SnakeToCamel(col.TableName)) + snaker.SnakeToCamel(col.ColumnName)
		enumName := ""
		enum, err := newEnumFromStrings(enumName, col.ColumnComment, enums)
		if nil != err {
			return err
		}

		s.Enums = append(s.Enums, enum)

		fieldType = enumName
	case "blob", "mediumblob", "longblob", "varbinary", "binary":
		fieldType = "bytes"
	case "date", "time", "datetime", "timestamp":
		//s.AppendImport("google/protobuf/timestamp.proto")
		fieldType = "int64"
	case "bool":
		fieldType = "bool"
	case "tinyint", "smallint", "int", "mediumint", "bigint":
		fieldType = "int64"
	case "decimal":
		fieldType = "string" // decimal diff float64  fix bug 2022-11-8
	case "double":
		fieldType = "float64" // fix bug 2022-11-8
	case "float":
		fieldType = "float64" // fix bug 2022-11-8
	}

	if "" == fieldType {
		return fmt.Errorf("no compatible go type found for `%s`. column: `%s`.`%s`", col.DataType, col.TableName, col.ColumnName)
	}

	field := NewMessageField(fieldType, col.ColumnName, col.ColumnComment, col.ColumnName)
	err := msg.AppendField(field)
	if nil != err {
		return err
	}

	return nil
}

func isInSlice(slice []string, s string) bool {
	for i := range slice {
		if strings.TrimSpace(slice[i]) == strings.TrimSpace(s) {
			return true
		}
	}
	return false
}

func FirstUpper(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func FirstToLower(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToLower(s[:1]) + s[1:]
}
