package authorization

const (
	ActionRunnerRead         Action = "runner.read"
	ActionRunnerRegister     Action = "runner.register"
	ActionRunnerDispatch     Action = "runner.dispatch"
	ActionRunnerRecordStatus Action = "runner.record_status"
	ActionRunnerSubmitResult Action = "runner.submit_result"
)

func init() {
	supportedActions[ActionRunnerRead] = struct{}{}
	supportedActions[ActionRunnerRegister] = struct{}{}
	supportedActions[ActionRunnerDispatch] = struct{}{}
	supportedActions[ActionRunnerRecordStatus] = struct{}{}
	supportedActions[ActionRunnerSubmitResult] = struct{}{}
}
