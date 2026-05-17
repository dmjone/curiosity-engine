// Package store is the only package that talks to Firestore.
//
// Firestore (Native mode) is the deliberate replacement for the idea note's
// "Redis + Postgres": both of those are always-on, always-billed servers,
// which would break the idle-cost-zero requirement. Firestore is serverless,
// scales to zero, and stays inside its perpetual free tier at this scale.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Scoring constants. Points are awarded per problem difficulty, plus a streak
// bonus that rewards daily consistency (the Duolingo hook) without runaway.
const (
	PointsEasy        = 10
	PointsMedium      = 20
	PointsHard        = 30
	PointsBoss        = 50
	StreakBonusPerDay = 2
	StreakBonusCap    = 30
)

// User is a participant's gamification record. Doc id is the Discord user id.
type User struct {
	ID            string    `firestore:"-"`
	Name          string    `firestore:"name"`
	Points        int       `firestore:"points"`
	CurrentStreak int       `firestore:"currentStreak"`
	LongestStreak int       `firestore:"longestStreak"`
	Solves        int       `firestore:"solves"`
	BossSolves    int       `firestore:"bossSolves"`
	FirstBloods   int       `firestore:"firstBloods"`
	LastSolved    string    `firestore:"lastSolved"` // YYYY-MM-DD in IST
	Created       time.Time `firestore:"created,serverTimestamp"`
	Updated       time.Time `firestore:"updated"`
}

// Problem is one day's challenge for one subject. Doc id is "<date>_<topic>".
// The Answer field never leaves the server: it is compared against, never sent.
type Problem struct {
	ID         string    `firestore:"-"`
	Date       string    `firestore:"date"`
	Topic      string    `firestore:"topic"`
	Title      string    `firestore:"title"`
	Statement  string    `firestore:"statement"`
	Difficulty string    `firestore:"difficulty"`
	Answer     string    `firestore:"answer"` // normalized expected answer
	Hints      []string  `firestore:"hints"`
	CO         string    `firestore:"co"` // tagged course outcome
	Points     int       `firestore:"points"`
	Solvers    []string  `firestore:"solvers"`
	Created    time.Time `firestore:"created,serverTimestamp"`
}

// State is the singleton self-update bookkeeping doc (meta/state).
type State struct {
	LastRunDate string    `firestore:"lastRunDate"` // last day the cron updated
	LastRunAt   time.Time `firestore:"lastRunAt"`
	Runs        int       `firestore:"runs"`
}

// SolveResult reports the outcome of an answer submission.
type SolveResult struct {
	Correct       bool
	AlreadySolved bool
	NoProblem     bool
	FirstBlood    bool
	PointsAwarded int
	NewPoints     int
	NewStreak     int
}

// Store wraps a Firestore client.
type Store struct {
	fs *firestore.Client
}

// New opens a Firestore client using Application Default Credentials.
func New(ctx context.Context, projectID, database string) (*Store, error) {
	var (
		fs  *firestore.Client
		err error
	)
	if database == "" || database == "(default)" {
		fs, err = firestore.NewClient(ctx, projectID)
	} else {
		fs, err = firestore.NewClientWithDatabase(ctx, projectID, database)
	}
	if err != nil {
		return nil, fmt.Errorf("firestore client: %w", err)
	}
	return &Store{fs: fs}, nil
}

// Close releases the Firestore client.
func (s *Store) Close() error { return s.fs.Close() }

// Normalize canonicalises an answer for comparison: lowercase, trimmed, with
// internal whitespace collapsed. Keeps answer-checking deterministic.
func Normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// GetUser returns a user, or (nil, nil) if they have no record yet.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	snap, err := s.fs.Collection("users").Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u User
	if err := snap.DataTo(&u); err != nil {
		return nil, err
	}
	u.ID = id
	return &u, nil
}

// Leaderboard returns the top users ordered by points descending.
func (s *Store) Leaderboard(ctx context.Context, limit int) ([]*User, error) {
	iter := s.fs.Collection("users").
		OrderBy("points", firestore.Desc).
		Limit(limit).
		Documents(ctx)
	defer iter.Stop()

	var out []*User
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var u User
		if err := doc.DataTo(&u); err != nil {
			return nil, err
		}
		u.ID = doc.Ref.ID
		out = append(out, &u)
	}
	return out, nil
}

// CountUsers returns how many participant records exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	iter := s.fs.Collection("users").DocumentRefs(ctx)
	n := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// GetProblem returns the problem for a date+topic, or (nil, nil) if none.
func (s *Store) GetProblem(ctx context.Context, date, topic string) (*Problem, error) {
	id := date + "_" + topic
	snap, err := s.fs.Collection("problems").Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Problem
	if err := snap.DataTo(&p); err != nil {
		return nil, err
	}
	p.ID = id
	return &p, nil
}

// SaveProblem writes a problem document.
func (s *Store) SaveProblem(ctx context.Context, p *Problem) error {
	p.ID = p.Date + "_" + p.Topic
	_, err := s.fs.Collection("problems").Doc(p.ID).Set(ctx, p)
	return err
}

// Channels returns the topic -> Discord channel id bindings.
func (s *Store) Channels(ctx context.Context) (map[string]string, error) {
	snap, err := s.fs.Collection("config").Doc("channels").Get(ctx)
	if status.Code(err) == codes.NotFound {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range snap.Data() {
		if sv, ok := v.(string); ok {
			out[k] = sv
		}
	}
	return out, nil
}

// SetChannel binds a topic to the Discord channel it should be posted in.
func (s *Store) SetChannel(ctx context.Context, topic, channelID string) error {
	_, err := s.fs.Collection("config").Doc("channels").
		Set(ctx, map[string]any{topic: channelID}, firestore.MergeAll)
	return err
}

// GetState returns the self-update bookkeeping doc (zero value if missing).
func (s *Store) GetState(ctx context.Context) (*State, error) {
	snap, err := s.fs.Collection("meta").Doc("state").Get(ctx)
	if status.Code(err) == codes.NotFound {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := snap.DataTo(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// SaveState persists the self-update bookkeeping doc.
func (s *Store) SaveState(ctx context.Context, st *State) error {
	_, err := s.fs.Collection("meta").Doc("state").Set(ctx, st)
	return err
}

// DecayStreaks resets the current streak of any user who did not solve a
// problem today or yesterday. Returns how many streaks were broken.
func (s *Store) DecayStreaks(ctx context.Context, today, yesterday string) (int, error) {
	iter := s.fs.Collection("users").
		Where("currentStreak", ">", 0).
		Documents(ctx)
	defer iter.Stop()

	broken := 0
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return broken, err
		}
		var u User
		if err := doc.DataTo(&u); err != nil {
			return broken, err
		}
		if u.LastSolved == today || u.LastSolved == yesterday {
			continue
		}
		_, err = doc.Ref.Update(ctx, []firestore.Update{
			{Path: "currentStreak", Value: 0},
			{Path: "updated", Value: time.Now()},
		})
		if err != nil {
			return broken, err
		}
		broken++
	}
	return broken, nil
}

// Solve atomically checks a submitted answer and, if correct, awards points
// and advances the user's streak. The whole read-validate-write cycle runs in
// one Firestore transaction so concurrent submissions cannot double-award.
func (s *Store) Solve(ctx context.Context, problemID, userID, userName, submitted, today, yesterday string) (*SolveResult, error) {
	res := &SolveResult{}
	err := s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		pRef := s.fs.Collection("problems").Doc(problemID)
		uRef := s.fs.Collection("users").Doc(userID)

		pSnap, err := tx.Get(pRef)
		if status.Code(err) == codes.NotFound {
			res.NoProblem = true
			return nil
		}
		if err != nil {
			return err
		}
		var p Problem
		if err := pSnap.DataTo(&p); err != nil {
			return err
		}

		var u User
		uSnap, err := tx.Get(uRef)
		if err == nil {
			if err := uSnap.DataTo(&u); err != nil {
				return err
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}

		for _, solver := range p.Solvers {
			if solver == userID {
				res.AlreadySolved = true
				return nil
			}
		}

		if Normalize(submitted) != p.Answer {
			res.Correct = false
			return nil
		}

		res.Correct = true
		res.FirstBlood = len(p.Solvers) == 0

		switch u.LastSolved {
		case today:
			// already solved another problem today; streak unchanged
		case yesterday:
			u.CurrentStreak++
		default:
			u.CurrentStreak = 1
		}
		if u.CurrentStreak > u.LongestStreak {
			u.LongestStreak = u.CurrentStreak
		}

		bonus := u.CurrentStreak * StreakBonusPerDay
		if bonus > StreakBonusCap {
			bonus = StreakBonusCap
		}
		award := p.Points + bonus

		u.Points += award
		u.Solves++
		if p.Difficulty == "boss" {
			u.BossSolves++
		}
		if res.FirstBlood {
			u.FirstBloods++
		}
		u.LastSolved = today
		u.Name = userName
		u.Updated = time.Now()

		p.Solvers = append(p.Solvers, userID)

		res.PointsAwarded = award
		res.NewPoints = u.Points
		res.NewStreak = u.CurrentStreak

		if err := tx.Set(pRef, &p); err != nil {
			return err
		}
		return tx.Set(uRef, &u)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}
