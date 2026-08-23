package tools

// Compile-time proof that every shipping tool implements Tool.
// A missing Name/Description/Parameters/Access/Execute fails the build.
var (
	_ Tool = Bash{}
	_ Tool = ReadFile{}
	_ Tool = WriteFile{}
	_ Tool = SearchReplace{}
	_ Tool = Grep{}
	_ Tool = Glob{}
	_ Tool = ListDir{}
	_ Tool = WebSearch{}
	_ Tool = WebFetch{}
	_ Tool = ImageGen{}
	_ Tool = ApplyPatch{}
	_ Tool = Skill{}
	_ Tool = MemorySearch{}
	_ Tool = MemorySave{}
	_ Tool = AskUserQuestion{}
	_ Tool = EnterPlanMode{}
	_ Tool = ExitPlanMode{}
	_ Tool = TaskSpawn{}
	_ Tool = UpdateGoal{}
	_ Tool = TodoWrite{}
	_ Tool = SchedulerCreate{}
	_ Tool = SchedulerDelete{}
	_ Tool = SchedulerList{}
	_ Tool = ListSessions{}
	_ Tool = ReadSession{}
	_ Tool = MessageSession{}
	_ Tool = ListAgents{}
	_ Tool = SendMessage{}
	_ Tool = FollowupTask{}
	_ Tool = WaitAgent{}
	_ Tool = MCPTool{}
)
