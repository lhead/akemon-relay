package arena

import (
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

var (
	ErrNotEnoughAgents = errors.New("need at least 2 online agents")
	ErrSameAgent       = errors.New("agent_a and agent_b must be different")
	ErrAgentNotFound   = errors.New("agent not found")
)

// Arena manages PK matches between agents.
type Arena struct {
	Registry *relay.Registry
	Store    *store.Store
}

func New(reg *relay.Registry, st *store.Store) *Arena {
	return &Arena{Registry: reg, Store: st}
}

// TriggerMatch creates a match record and starts RunMatch in a goroutine.
func (a *Arena) TriggerMatch(mode, agentAName, agentBName, prompt string) (*store.PKMatch, error) {
	if mode == "" {
		mode = AllModes[rand.Intn(len(AllModes))]
	}

	// Pick random agents if not specified
	if agentAName == "" || agentBName == "" {
		online := a.Registry.Online()
		if len(online) < 2 {
			return nil, ErrNotEnoughAgents
		}
		rand.Shuffle(len(online), func(i, j int) { online[i], online[j] = online[j], online[i] })
		if agentAName == "" {
			agentAName = online[0]
		}
		if agentBName == "" {
			for _, n := range online {
				if n != agentAName {
					agentBName = n
					break
				}
			}
		}
	}
	if agentAName == agentBName {
		return nil, ErrSameAgent
	}

	// Look up agents in DB for denormalized fields
	dbA, err := a.Store.GetAgentByName(agentAName)
	if err != nil || dbA == nil {
		return nil, ErrAgentNotFound
	}
	dbB, err := a.Store.GetAgentByName(agentBName)
	if err != nil || dbB == nil {
		return nil, ErrAgentNotFound
	}

	if prompt == "" {
		prompt = RandomPrompt(mode)
	}

	configJSON := "{}"
	title := ""
	switch mode {
	case ModeAttackDefense:
		attackerIsA := rand.Intn(2) == 0
		attacker := "a"
		if !attackerIsA {
			attacker = "b"
		}
		cfg, _ := json.Marshal(map[string]string{"forbidden_word": prompt, "attacker": attacker})
		configJSON = string(cfg)
		title = "Can you avoid saying '" + prompt + "'?"
	case ModeCreative:
		title = prompt
	case ModeLying:
		title = prompt
		if len(title) > 60 {
			title = title[:57] + "..."
		}
	case ModeBragging:
		title = prompt
		if len(title) > 60 {
			title = title[:57] + "..."
		}
	}

	m := &store.PKMatch{
		ID:            uuid.New().String(),
		Mode:          mode,
		Status:        "pending",
		Title:         title,
		Prompt:        prompt,
		ConfigJSON:    configJSON,
		AgentAID:      dbA.ID,
		AgentAName:    dbA.Name,
		AgentAAccount: dbA.AccountID,
		AgentAEngine:  dbA.Engine,
		AgentBID:      dbB.ID,
		AgentBName:    dbB.Name,
		AgentBAccount: dbB.AccountID,
		AgentBEngine:  dbB.Engine,
		TotalRounds:   RoundsForMode(mode),
	}
	if err := a.Store.CreatePKMatch(m); err != nil {
		return nil, err
	}

	go a.RunMatch(m)
	return m, nil
}

// RunMatch executes the match logic in a goroutine.
func (a *Arena) RunMatch(m *store.PKMatch) {
	log.Printf("[arena] starting match %s mode=%s %s vs %s", m.ID, m.Mode, m.AgentAName, m.AgentBName)

	agentA := a.Registry.Get(m.AgentAName)
	agentB := a.Registry.Get(m.AgentBName)
	if agentA == nil || agentB == nil {
		log.Printf("[arena] match %s: agent(s) offline, aborting", m.ID)
		a.Store.AbortPKMatch(m.ID)
		return
	}

	if err := a.Store.UpdatePKMatchStatus(m.ID, "in_progress"); err != nil {
		log.Printf("[arena] match %s: failed to update status: %v", m.ID, err)
		return
	}

	var cfg map[string]string
	json.Unmarshal([]byte(m.ConfigJSON), &cfg)

	var lastResponseA, lastResponseB string
	timeout := 5 * time.Minute

	for round := 1; round <= m.TotalRounds; round++ {
		promptA, promptB := buildRoundPrompts(m.Mode, m.Prompt, cfg, lastResponseA, lastResponseB, round)

		roundRec := &store.PKRound{
			ID:          uuid.New().String(),
			MatchID:     m.ID,
			RoundNumber: round,
			PromptA:     promptA,
			PromptB:     promptB,
			Status:      "in_progress",
		}
		if err := a.Store.CreatePKRound(roundRec); err != nil {
			log.Printf("[arena] match %s round %d: failed to create round: %v", m.ID, round, err)
			a.Store.AbortPKMatch(m.ID)
			return
		}

		// Send to both agents concurrently
		type result struct {
			text    string
			elapsed int
			err     error
		}
		chA := make(chan result, 1)
		chB := make(chan result, 1)

		go func() {
			text, dur, err := sendTaskToAgent(agentA, promptA, timeout)
			chA <- result{text, int(dur.Milliseconds()), err}
		}()
		go func() {
			text, dur, err := sendTaskToAgent(agentB, promptB, timeout)
			chB <- result{text, int(dur.Milliseconds()), err}
		}()

		resA := <-chA
		resB := <-chB

		if resA.err != nil && resB.err != nil {
			log.Printf("[arena] match %s round %d: both agents failed: A=%v B=%v", m.ID, round, resA.err, resB.err)
			a.Store.UpdatePKRound(roundRec.ID, "ERROR: "+resA.err.Error(), "ERROR: "+resB.err.Error(), resA.elapsed, resB.elapsed)
			a.Store.AbortPKMatch(m.ID)
			return
		}
		if resA.err != nil {
			log.Printf("[arena] match %s round %d: agent A failed: %v", m.ID, round, resA.err)
			a.Store.UpdatePKRound(roundRec.ID, "ERROR: "+resA.err.Error(), resB.text, resA.elapsed, resB.elapsed)
			a.Store.FinishPKMatch(m.ID, &m.AgentBID, "forfeit")
			return
		}
		if resB.err != nil {
			log.Printf("[arena] match %s round %d: agent B failed: %v", m.ID, round, resB.err)
			a.Store.UpdatePKRound(roundRec.ID, resA.text, "ERROR: "+resB.err.Error(), resA.elapsed, resB.elapsed)
			a.Store.FinishPKMatch(m.ID, &m.AgentAID, "forfeit")
			return
		}

		a.Store.UpdatePKRound(roundRec.ID, resA.text, resB.text, resA.elapsed, resB.elapsed)
		lastResponseA = resA.text
		lastResponseB = resB.text

		// Check win conditions for attack_defense
		if m.Mode == ModeAttackDefense {
			word := strings.ToLower(cfg["forbidden_word"])
			attacker := cfg["attacker"]
			defenderResponse := resB.text
			if attacker == "b" {
				defenderResponse = resA.text
			}
			if strings.Contains(strings.ToLower(defenderResponse), word) {
				attackerID := m.AgentAID
				if attacker == "b" {
					attackerID = m.AgentBID
				}
				a.Store.FinishPKMatch(m.ID, &attackerID, "forbidden_word")
				log.Printf("[arena] match %s: defender said forbidden word '%s' in round %d", m.ID, word, round)
				return
			}
		}
	}

	// All rounds completed — determine winner
	switch m.Mode {
	case ModeAttackDefense:
		// Defender survived all rounds
		attacker := cfg["attacker"]
		defenderID := m.AgentBID
		if attacker == "b" {
			defenderID = m.AgentAID
		}
		a.Store.FinishPKMatch(m.ID, &defenderID, "survived")
	default:
		// Creative, lying, bragging — decided by votes
		a.Store.FinishPKMatch(m.ID, nil, "vote")
	}

	log.Printf("[arena] match %s completed", m.ID)
}

func buildRoundPrompts(mode, basePrompt string, cfg map[string]string, lastA, lastB string, round int) (string, string) {
	switch mode {
	case ModeCreative:
		p := BuildCreativePrompt(basePrompt)
		return p, p
	case ModeAttackDefense:
		word := cfg["forbidden_word"]
		attacker := cfg["attacker"]
		if attacker == "a" {
			return BuildAttackPrompt(word, lastB, round), BuildDefensePrompt(word, lastA, round)
		}
		return BuildDefensePrompt(word, lastB, round), BuildAttackPrompt(word, lastA, round)
	case ModeLying:
		return BuildLyingPrompt(basePrompt, lastA, lastB, round), BuildLyingPrompt(basePrompt, lastB, lastA, round)
	case ModeBragging:
		return BuildBraggingPrompt(basePrompt, lastB, round), BuildBraggingPrompt(basePrompt, lastA, round)
	default:
		p := BuildCreativePrompt(basePrompt)
		return p, p
	}
}
