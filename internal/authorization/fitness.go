package authorization

const (
	ActionFitnessRead            Action = "fitness.read"
	ActionFitnessRecord          Action = "fitness.record"
	ActionFitnessConfigurePolicy Action = "fitness.configure_policy"
	ActionFitnessCompare         Action = "fitness.compare"
)

func init() {
	supportedActions[ActionFitnessRead] = struct{}{}
	supportedActions[ActionFitnessRecord] = struct{}{}
	supportedActions[ActionFitnessConfigurePolicy] = struct{}{}
	supportedActions[ActionFitnessCompare] = struct{}{}
}
