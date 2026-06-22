package service

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestDeduplicateDuplicatedArguments(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "empty",
			input:   "",
			want:    "",
			changed: false,
		},
		{
			name:    "odd_length",
			input:   `{"a":1}`,
			want:    `{"a":1}`,
			changed: false,
		},
		{
			name:    "not_duplicated",
			input:   `{"cmd":"echo hi"}`,
			want:    `{"cmd":"echo hi"}`,
			changed: false,
		},
		{
			name:    "duplicated_json_object",
			input:   `{"cmd":"echo hi"}{"cmd":"echo hi"}`,
			want:    `{"cmd":"echo hi"}`,
			changed: true,
		},
		{
			name:    "duplicated_with_whitespace_trimmed",
			input:   `  {"cmd":"echo hi"}{"cmd":"echo hi"}  `,
			want:    `{"cmd":"echo hi"}`,
			changed: true,
		},
		{
			name:    "duplicated_but_first_half_not_valid_json",
			input:   `abcabc`,
			want:    `abcabc`,
			changed: false,
		},
		{
			name:    "duplicated_json_array",
			input:   `[1,2,3][1,2,3]`,
			want:    `[1,2,3]`,
			changed: true,
		},
		{
			name:    "single_char_not_duplicated",
			input:   `{}`,
			want:    `{}`,
			changed: false,
		},
		{
			name:    "duplicated_empty_object",
			input:   `{}{}`,
			want:    `{}`,
			changed: true,
		},
		{
			name:    "real_codex_exec_command_duplicated",
			input:   `{"command":"echo hi","workdir":"/tmp"}{"command":"echo hi","workdir":"/tmp"}`,
			want:    `{"command":"echo hi","workdir":"/tmp"}`,
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := deduplicateDuplicatedArguments(tt.input)
			if changed != tt.changed {
				t.Errorf("deduplicateDuplicatedArguments(%q) changed = %v, want %v", tt.input, changed, tt.changed)
			}
			if got != tt.want {
				t.Errorf("deduplicateDuplicatedArguments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDedupResponsesSSEArgumentsLine(t *testing.T) {
	// A function_call_arguments.done event with duplicated arguments.
	dupedArgs := `{"cmd":"echo hi"}{"cmd":"echo hi"}`
	sseLine := "data: " + makeFunctionCallArgsDoneEvent("call_abc", "exec_command", dupedArgs)

	corrected, changed := dedupResponsesSSEArgumentsLine(sseLine)
	if !changed {
		t.Fatal("expected changed=true for duplicated arguments SSE line")
	}

	data, ok := extractOpenAISSEDataLine(corrected)
	if !ok {
		t.Fatal("corrected line is not a valid SSE data line")
	}

	args := gjson.Get(data, "arguments").String()
	if args != `{"cmd":"echo hi"}` {
		t.Errorf("expected deduplicated arguments, got %q", args)
	}
}

func TestDedupResponsesSSEArgumentsLine_NoChange(t *testing.T) {
	// A function_call_arguments.done event with normal (non-duplicated) arguments.
	normalArgs := `{"cmd":"echo hi"}`
	sseLine := "data: " + makeFunctionCallArgsDoneEvent("call_abc", "exec_command", normalArgs)

	_, changed := dedupResponsesSSEArgumentsLine(sseLine)
	if changed {
		t.Fatal("expected changed=false for non-duplicated arguments SSE line")
	}
}

func TestDedupResponsesSSEArgumentsLine_OutputItemDone(t *testing.T) {
	// An output_item.done event with a function_call item containing duplicated arguments.
	dupedArgs := `{"cmd":"ls"}{"cmd":"ls"}`
	sseLine := "data: " + makeOutputItemDoneEvent("fc_1", "function_call", "call_abc", "exec_command", dupedArgs)

	corrected, changed := dedupResponsesSSEArgumentsLine(sseLine)
	if !changed {
		t.Fatal("expected changed=true for duplicated arguments in output_item.done")
	}

	data, ok := extractOpenAISSEDataLine(corrected)
	if !ok {
		t.Fatal("corrected line is not a valid SSE data line")
	}

	args := gjson.Get(data, "item.arguments").String()
	if args != `{"cmd":"ls"}` {
		t.Errorf("expected deduplicated arguments, got %q", args)
	}
}

func TestDedupResponsesSSEArgumentsLine_NonDataLine(t *testing.T) {
	line := "event: response.function_call_arguments.done"
	_, changed := dedupResponsesSSEArgumentsLine(line)
	if changed {
		t.Fatal("expected changed=false for non-data SSE line")
	}
}

func TestDedupResponsesSSEArgumentsLine_DoneMarker(t *testing.T) {
	_, changed := dedupResponsesSSEArgumentsLine("data: [DONE]")
	if changed {
		t.Fatal("expected changed=false for [DONE] line")
	}
}

func TestDedupResponsesSSEArgumentsLine_UnknownEventType(t *testing.T) {
	line := `data: {"type":"response.created","response":{"id":"resp_1"}}`
	_, changed := dedupResponsesSSEArgumentsLine(line)
	if changed {
		t.Fatal("expected changed=false for unknown event type")
	}
}

func TestDedupResponsesBodyArguments(t *testing.T) {
	dupedArgs := `{"command":"echo hello","workdir":"/root"}{"command":"echo hello","workdir":"/root"}`
	body := makeNonStreamingResponseBody("function_call", "call_abc", "exec_command", dupedArgs)

	corrected, changed := dedupResponsesBodyArguments(body)
	if !changed {
		t.Fatal("expected changed=true for duplicated arguments in response body")
	}

	args := gjson.GetBytes(corrected, "output.0.arguments").String()
	if args != `{"command":"echo hello","workdir":"/root"}` {
		t.Errorf("expected deduplicated arguments, got %q", args)
	}
}

func TestDedupResponsesBodyArguments_NoChange(t *testing.T) {
	normalArgs := `{"command":"echo hello","workdir":"/root"}`
	body := makeNonStreamingResponseBody("function_call", "call_abc", "exec_command", normalArgs)

	_, changed := dedupResponsesBodyArguments(body)
	if changed {
		t.Fatal("expected changed=false for non-duplicated arguments in response body")
	}
}

func TestDedupResponsesBodyArguments_CustomToolCall(t *testing.T) {
	dupedInput := `{"query":"test"}{"query":"test"}`
	body := makeNonStreamingResponseBody("custom_tool_call", "call_xyz", "search", dupedInput)

	corrected, changed := dedupResponsesBodyArguments(body)
	if !changed {
		t.Fatal("expected changed=true for duplicated input in custom_tool_call")
	}

	input := gjson.GetBytes(corrected, "output.0.input").String()
	if input != `{"query":"test"}` {
		t.Errorf("expected deduplicated input, got %q", input)
	}
}

func TestDedupResponsesBodyArguments_EmptyBody(t *testing.T) {
	_, changed := dedupResponsesBodyArguments(nil)
	if changed {
		t.Fatal("expected changed=false for empty body")
	}
}

func TestDedupResponsesBodyArguments_NoOutput(t *testing.T) {
	body := []byte(`{"id":"resp_1","object":"response","status":"completed"}`)
	_, changed := dedupResponsesBodyArguments(body)
	if changed {
		t.Fatal("expected changed=false for body without output array")
	}
}

// makeFunctionCallArgsDoneEvent builds a response.function_call_arguments.done
// SSE event JSON string for testing.
func makeFunctionCallArgsDoneEvent(callID, name, args string) string {
	return `{"type":"response.function_call_arguments.done","sequence_number":42,"output_index":0,"item_id":"fc_1","call_id":"` + callID + `","name":"` + name + `","arguments":` + jsonString(args) + `}`
}

// makeOutputItemDoneEvent builds a response.output_item.done SSE event JSON
// string for testing.
func makeOutputItemDoneEvent(itemID, itemType, callID, name, args string) string {
	return `{"type":"response.output_item.done","sequence_number":43,"output_index":0,"item":{"type":"` + itemType + `","id":"` + itemID + `","status":"completed","call_id":"` + callID + `","name":"` + name + `","arguments":` + jsonString(args) + `}}`
}

// makeNonStreamingResponseBody builds a non-streaming Responses API JSON body
// for testing.
func makeNonStreamingResponseBody(itemType, callID, name, args string) []byte {
	argsField := "arguments"
	if itemType == "custom_tool_call" {
		argsField = "input"
	}
	return []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"` + itemType + `","id":"fc_1","status":"completed","call_id":"` + callID + `","name":"` + name + `","` + argsField + `":` + jsonString(args) + `}]}`)
}

// jsonString encodes a string as a JSON string literal (with surrounding quotes).
func jsonString(s string) string {
	var buf []byte
	buf = append(buf, '"')
	for _, r := range s {
		switch r {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		default:
			buf = append(buf, string(r)...)
		}
	}
	buf = append(buf, '"')
	return string(buf)
}
