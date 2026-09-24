package types

// Kind classifies types.
type Kind int

const (
	KAny Kind = iota
	KUnknown
	KNever
	KVoid
	KNil
	KBoolean
	KNumber
	KInteger
	KFloat
	KString
  KTable
  KThread
  KUserdata
  KCdata
  KCtype
  KFunction
	KLiteral
	KUnion
	KIntersection
	KArray
	KMap
	KTuple
	KFunc
	KObject
	KEnum
	KGenericParam
	KRef
	KClass
	KClassValue
	KConditional
	KInferParam
	KMapped
	KIndexedAccess
	KChan
)

type Type interface {
	Kind() Kind
	String() string
}

// Access is member visibility (classes only; plain objects are always public).
type Access int

const (
	Public Access = iota
	Private
	Protected
)

func (a Access) String() string {
	switch a {
	case Private:
		return "private"
	case Protected:
		return "protected"
	}
	return "public"
}

type Field struct {
	Type     Type
	Optional bool
	Readonly bool
	Access   Access
	Owner    *ClassDef // defining class for class members; nil otherwise
}

type Indexer struct {
	Key   Type
	Value Type
}

type Primitive struct {
	K    Kind
	Name string
}

func (t *Primitive) Kind() Kind    { return t.K }
func (t *Primitive) String() string { return t.Name }
