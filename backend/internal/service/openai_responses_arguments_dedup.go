package service

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// deduplicateDuplicatedArguments checks if a string value is a duplicated JSON
// payload (X+X pattern where the first half equals the second half) and returns
// only X if so.
//
// This is a defensive measure against upstream models that accidentally
// duplicate the `arguments` (or `input`) field in
// `response.function_call_arguments.done` events. When the duplicated value
// reaches strict clients like Codex CLI, the JSON parser successfully parses the
// first object and then reports "trailing characters at line 1 column N" at the
// start of the second (duplicate) object.
//
// The check is conservative: it only deduplicates when:
//  1. The string length is even.
//  2. The first half is byte-equal to the second half.
//  3. The first half is itself valid JSON.
//
// This avoids any risk of corrupting legitimate arguments that happen to have an
// even length.
func deduplicateDuplicatedArguments(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := len(s)
	if n < 2 || n%2 != 0 {
		return s, false
	}
	half := n / 2
	first := s[:half]
	second := s[half:]
	if first != second {
		return s, false
	}
	if !gjson.Valid(first) {
		return s, false
	}
	return first, true
}

// responsesSSEArgumentsEventTypes lists SSE event types whose JSON payload may
// carry a string-valued arguments/input field that can be duplicated by a
// misbehaving upstream.
var responsesSSEArgumentsEventTypes = map[string]string{
	"response.function_call_arguments.done": "arguments",
	"response.custom_tool_call_input.done":  "input",
	"response.output_item.done":             "item.arguments",
}

// dedupResponsesSSEArgumentsLine scans an SSE `data:` line for duplicated
// arguments/input values in known Responses API event types and returns the
// corrected line along with whether a correction was applied.
//
// The line is expected to be a full SSE line beginning with "data:".
func dedupResponsesSSEArgumentsLine(line string) (string, bool) {
	data, ok := extractOpenAISSEDataLine(line)
	if !ok {
		return line, false
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[DONE]" || !gjson.Valid(trimmed) {
		return line, false
	}

	eventType := strings.TrimSpace(gjson.Get(trimmed, "type").String())
	argsPath, known := responsesSSEArgumentsEventTypes[eventType]
	if !known {
		return line, false
	}

	argsResult := gjson.Get(trimmed, argsPath)
	if !argsResult.Exists() || argsResult.Type != gjson.String {
		return line, false
	}

	original := argsResult.String()
	deduped, changed := deduplicateDuplicatedArguments(original)
	if !changed {
		return line, false
	}

	newData, err := sjson.Set(trimmed, argsPath, deduped)
	if err != nil {
		return line, false
	}

	logger.LegacyPrintf("service.openai_responses_arguments_dedup",
		"[ResponsesArgsDedup] Deduplicated duplicated arguments in SSE event type=%s: original_len=%d deduped_len=%d",
		eventType, len(original), len(deduped))

	return "data: " + newData, true
}

// dedupResponsesBodyArguments walks a non-streaming Responses JSON body and
// deduplicates any duplicated `arguments` string field found in function_call
// or custom_tool_call output items. Returns the corrected body and whether a
// correction was applied.
func dedupResponsesBodyArguments(body []byte) ([]byte, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false
	}

	outputArr := gjson.GetBytes(body, "output")
	if !outputArr.Exists() || !outputArr.IsArray() {
		return body, false
	}

	outputItems := outputArr.Array()
	updated := body
	corrected := false

	for i := 0; i < len(outputItems); i++ {
		itemPath := "output." + strconv.Itoa(i)
		itemType := strings.TrimSpace(gjson.GetBytes(updated, itemPath+".type").String())

		var argsPath string
		switch itemType {
		case "function_call":
			argsPath = itemPath + ".arguments"
		case "custom_tool_call":
			argsPath = itemPath + ".input"
		default:
			continue
		}

		argsResult := gjson.GetBytes(updated, argsPath)
		if !argsResult.Exists() || argsResult.Type != gjson.String {
			continue
		}

		original := argsResult.String()
		deduped, changed := deduplicateDuplicatedArguments(original)
		if !changed {
			continue
		}

		next, err := sjson.SetBytes(updated, argsPath, deduped)
		if err != nil {
			continue
		}
		updated = next
		corrected = true

		logger.LegacyPrintf("service.openai_responses_arguments_dedup",
			"[ResponsesArgsDedup] Deduplicated duplicated arguments in response body item type=%s index=%d: original_len=%d deduped_len=%d",
			itemType, i, len(original), len(deduped))
	}

	if !corrected {
		return body, false
	}
	return updated, true
}


