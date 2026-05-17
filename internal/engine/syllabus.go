package engine

// Subject is one CSE course tracked by CuriosityEngine. Topic is the stable
// key used for channel bindings and problem ids; it should match the Discord
// channel theme (e.g. an #operating-systems channel).
type Subject struct {
	Topic       string
	DisplayName string
	Outcomes    []Outcome
}

// Outcome ties a concept to an official course-outcome code. Every generated
// problem is tagged with one, so students doing the challenge are provably
// doing mapped coursework, not extra work.
type Outcome struct {
	CO      string
	Concept string
}

// Syllabus is the seeded CSE course map. It is intentionally code, not data:
// it changes rarely, needs no admin UI, and keeps the service self-contained.
var Syllabus = []Subject{
	{
		Topic:       "operating-systems",
		DisplayName: "Operating Systems",
		Outcomes: []Outcome{
			{"OS-CO1", "process scheduling algorithms and CPU burst analysis"},
			{"OS-CO2", "memory management, paging and virtual address translation"},
			{"OS-CO3", "concurrency, synchronization primitives and deadlock"},
			{"OS-CO4", "file systems, disk scheduling and storage allocation"},
		},
	},
	{
		Topic:       "compilers",
		DisplayName: "Compiler Design",
		Outcomes: []Outcome{
			{"CD-CO1", "lexical analysis and finite-automata construction"},
			{"CD-CO2", "context-free grammars and parsing"},
			{"CD-CO3", "syntax-directed translation and semantic analysis"},
			{"CD-CO4", "intermediate code generation and optimization"},
		},
	},
	{
		Topic:       "linear-algebra",
		DisplayName: "Linear Algebra",
		Outcomes: []Outcome{
			{"LA-CO1", "matrix operations and systems of linear equations"},
			{"LA-CO2", "vector spaces, basis and rank"},
			{"LA-CO3", "eigenvalues, eigenvectors and diagonalization"},
			{"LA-CO4", "linear transformations and orthogonality"},
		},
	},
	{
		Topic:       "data-structures",
		DisplayName: "Data Structures & Algorithms",
		Outcomes: []Outcome{
			{"DS-CO1", "arrays, linked lists and amortized analysis"},
			{"DS-CO2", "trees, balanced search trees and heaps"},
			{"DS-CO3", "graphs, traversal and shortest paths"},
			{"DS-CO4", "hashing, tries and algorithmic complexity"},
		},
	},
	{
		Topic:       "computer-networks",
		DisplayName: "Computer Networks",
		Outcomes: []Outcome{
			{"CN-CO1", "the TCP/IP stack and layered protocol design"},
			{"CN-CO2", "IP addressing, subnetting and routing"},
			{"CN-CO3", "transport-layer reliability, flow and congestion control"},
			{"CN-CO4", "application-layer protocols and the DNS"},
		},
	},
	{
		Topic:       "dbms",
		DisplayName: "Database Management Systems",
		Outcomes: []Outcome{
			{"DB-CO1", "the relational model and relational algebra"},
			{"DB-CO2", "SQL query design and evaluation"},
			{"DB-CO3", "functional dependencies and normalization"},
			{"DB-CO4", "transactions, concurrency control and indexing"},
		},
	},
}

// SubjectByTopic returns the Subject for a topic key.
func SubjectByTopic(topic string) (Subject, bool) {
	for _, s := range Syllabus {
		if s.Topic == topic {
			return s, true
		}
	}
	return Subject{}, false
}
