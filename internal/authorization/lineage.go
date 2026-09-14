package authorization

const (
	ActionLineageRead   Action = "lineage.read"
	ActionLineageWrite  Action = "lineage.write"
	ActionLineageDecide Action = "lineage.decide"
)

func init() {
	supportedActions[ActionLineageRead] = struct{}{}
	supportedActions[ActionLineageWrite] = struct{}{}
	supportedActions[ActionLineageDecide] = struct{}{}
}
