//! Compatibility re-export of the shared MCP structured-content renderer.

pub use xai_tool_runtime::render_structured_content;

#[cfg(test)]
#[path = "mcp_structured_content_tests.rs"]
mod tests;
