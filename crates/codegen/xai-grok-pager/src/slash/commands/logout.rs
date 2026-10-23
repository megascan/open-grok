//! `/logout` -- remove auth credentials and return to the login screen.

use crate::app::actions::Action;
use crate::slash::command::{AppCtx, ArgItem, CommandExecCtx, CommandResult, SlashCommand};

pub struct LogoutCommand;

impl SlashCommand for LogoutCommand {
    fn name(&self) -> &str {
        "logout"
    }

    fn description(&self) -> &str {
        "Log out of xAI, OpenAI Codex, or Antigravity"
    }

    fn usage(&self) -> &str {
        "/logout [codex|antigravity]"
    }

    fn takes_args(&self) -> bool {
        true
    }

    fn arg_placeholder(&self) -> Option<&str> {
        Some("account")
    }

    fn suggest_args(&self, _ctx: &AppCtx, _args_query: &str) -> Option<Vec<ArgItem>> {
        Some(vec![
            ArgItem {
                display: "codex".to_string(),
                match_text: "codex openai chatgpt".to_string(),
                insert_text: "codex".to_string(),
                description: "Disconnect the OpenAI Codex account".to_string(),
            },
            ArgItem {
                display: "antigravity".to_string(),
                match_text: "antigravity google".to_string(),
                insert_text: "antigravity".to_string(),
                description: "Disconnect the Antigravity account".to_string(),
            },
        ])
    }

    fn run(&self, _ctx: &mut CommandExecCtx, args: &str) -> CommandResult {
        match args.trim() {
            "" => CommandResult::Action(Action::Logout),
            "codex" => CommandResult::Action(Action::LogoutCodex),
            "antigravity" => CommandResult::Action(Action::LogoutAntigravity),
            arg => CommandResult::Error(format!(
                "Unknown account: {arg}. Use /logout, /logout codex, or /logout antigravity"
            )),
        }
    }
}
