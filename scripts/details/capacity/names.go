package capacity

// Kubernetes' own names for what a snapshot holds and a trial loads, said
// once here for everything that reads or writes either.
const (
	KindPod           = "Pod"
	KindVolume        = "PersistentVolume"
	KindClaim         = "PersistentVolumeClaim"
	KindPriorityClass = "PriorityClass"

	// Where a pod names a claim, a class of storage, and its containers
	// that run before the rest; where a set gives each of its pods a claim
	// of its own; and where a volume or a pod says which machines.
	ClaimKey          = "persistentVolumeClaim"
	StorageClassKey   = "storageClassName"
	InitContainersKey = "initContainers"
	ClaimTemplatesKey = "volumeClaimTemplates"
	NodeAffinityKey   = "nodeAffinity"

	// How a pod says which machines it must be on: the rule that holds while
	// it is being placed, its terms, what each term matches by, and the
	// operator for "has no such label".
	RequiredKey         = "requiredDuringSchedulingIgnoredDuringExecution"
	SelectorTermsKey    = "nodeSelectorTerms"
	MatchExpressionsKey = "matchExpressions"
	HasNoLabel          = "DoesNotExist"

	// EveryNamespace has kubectl list a kind wherever it is.
	EveryNamespace = "--all-namespaces"
	// Nothing is a list with nothing in it, as kubectl gives one.
	Nothing = `{"items":[]}`

	// APIServer and Scheduler are the two programs of Kubernetes' own that a
	// change is tried with, by the names they are published and run under.
	APIServer = "kube-apiserver"
	Scheduler = "kube-scheduler"
)
