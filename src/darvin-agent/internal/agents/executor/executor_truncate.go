// Tool-result truncation applied to what the store persists.

package executor

// maxToolResultStoreBytes caps the tool result persisted into
// ToolCall.Result. Bash / read output can reach the megabyte range;
// the store only keeps the truncated prefix plus a tail marker; live
// streaming events still push the full content to the renderer, so
// truncation only affects tool-result display on reload.
const maxToolResultStoreBytes = 64 * 1024

const toolResultTruncatedSuffix = "\n…[truncated]"

func truncateForStore(content string) string {
	if len(content) <= maxToolResultStoreBytes {
		return content
	}
	keep := maxToolResultStoreBytes - len(toolResultTruncatedSuffix)
	if keep < 0 {
		keep = 0
	}
	return content[:keep] + toolResultTruncatedSuffix
}
