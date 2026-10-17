//! `/resume` -- open session picker overlay to resume a previous session.

use crate::app::actions::Action;
use crate::slash::command::{CommandExecCtx, CommandResult, SlashCommand};

pub struct ResumeCommand;

impl SlashCommand for ResumeCommand {
    fn name(&self) -> &str {
        "resume"
    }

    fn description(&self) -> &str {
        "Resume a previous session"
    }

    fn usage(&self) -> &str {
        "/resume [id or name]"
    }

    fn takes_args(&self) -> bool {
        true
    }

    fn run(&self, _ctx: &mut CommandExecCtx, args: &str) -> CommandResult {
        let args = args.trim();
        CommandResult::Action(if args.is_empty() {
            Action::ShowSessionPicker
        } else {
            Action::ShowSessionPickerFor(args.to_owned())
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::acp::model_state::ModelState;

    #[test]
    fn resume_passes_argument_and_preserves_no_argument_behavior() {
        let models = ModelState::default();
        let bundle = crate::app::bundle::BundleState {
            has_cache: false,
            version: String::new(),
            personas: Vec::new(),
            roles: Vec::new(),
            agents: Vec::new(),
            skills: Vec::new(),
            persona_details: Vec::new(),
            role_details: Vec::new(),
        };
        assert!(ResumeCommand.takes_args());
        assert!(!ResumeCommand.args_required());
        let mut ctx = CommandExecCtx {
            models: &models,
            session_id: None,
            bundle_state: &bundle,
            screen_mode: crate::app::ScreenMode::Inline,
            billing_surface_visible: true,
            pager_state: crate::settings::PagerLocalSnapshot::default(),
        };
        assert!(matches!(
            ResumeCommand.run(&mut ctx, "  "),
            CommandResult::Action(Action::ShowSessionPicker)
        ));
        assert!(
            matches!(ResumeCommand.run(&mut ctx, "  A Multi Word Title  "), CommandResult::Action(Action::ShowSessionPickerFor(value)) if value == "A Multi Word Title")
        );
    }
}
