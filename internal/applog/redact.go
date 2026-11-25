package applog

import (
	"bytes"
	"encoding/json"

	"alive/internal/diag"
)

// decodeLine 把一行 JSON 日志解析成顶层字段表。
//
// UseNumber：数字原样保留，不让毫秒数、时间戳在 float64 里丢精度。
// 解析失败（不是对象、被截断）返回 nil——脱敏漏掉的代价比少一条日志大得多。
func decodeLine(line []byte) map[string]any {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil
	}
	return obj
}

// encodeLine 把字段表写回一行 JSON。
func encodeLine(obj map[string]any) []byte {
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return append(out, '\n')
}

// redactValue 递归地对每个字符串做脱敏，原地改写。
//
// 为什么在这里，而不像以前那样在 slog handler 里换属性：slog 的 ReplaceAttr
// 会对每个属性过一遍，zerolog 没有等价物——它的 Hook 只能追加字段，改不了
// 已经写进去的值。于是「唯一脱敏出口」这条不变量换个位置守：落盘前的最后一道。
//
// 位置变了，性质没变：它仍然不依赖调用点自觉，对所有字符串一视同仁。
func redactValue(v any) any {
	switch t := v.(type) {
	case string:
		return diag.Sanitize(t)
	case map[string]any:
		for k, sub := range t {
			t[k] = redactValue(sub)
		}
		return t
	case []any:
		for i, sub := range t {
			t[i] = redactValue(sub)
		}
		return t
	default:
		return v
	}
}
