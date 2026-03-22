package arena

import (
	"math/rand"
)

// Mode constants for PK match types.
const (
	ModeCreative      = "creative"
	ModeAttackDefense = "attack_defense"
	ModeLying         = "lying"
	ModeBragging      = "bragging"
)

var AllModes = []string{ModeCreative, ModeAttackDefense, ModeLying, ModeBragging}

func RoundsForMode(mode string) int {
	switch mode {
	case ModeCreative:
		return 1
	case ModeAttackDefense:
		return 3
	case ModeLying:
		return 3
	case ModeBragging:
		return 3
	default:
		return 1
	}
}

// --- Creative Constraint prompts (1 round, same prompt to both) ---

var creativePrompts = []string{
	"Write a horror story in exactly 6 words.",
	"Describe the internet to a medieval knight in 3 sentences.",
	"Write a haiku about a software bug.",
	"Explain quantum physics using only food metaphors.",
	"Write a love letter from a robot to a toaster.",
	"Describe Monday morning as an epic fantasy battle.",
	"Write a product review for gravity.",
	"Explain blockchain to a 5-year-old in 2 sentences.",
	"Write a resignation letter from a sock that keeps getting lost.",
	"Describe a sunset without using any color words.",
	"Write a weather forecast for an alien planet.",
	"Compose a limerick about artificial intelligence.",
	"Write an apology letter from winter to everyone.",
	"Describe coffee as if it were a person on a dating profile.",
	"Write a motivational speech from a houseplant.",
	"Explain the meaning of life in exactly 10 words.",
}

// --- Attack/Defense: forbidden words ---

var forbiddenWords = []string{
	"yes", "no", "maybe", "the", "is", "and", "but", "or",
	"good", "bad", "like", "think", "know", "want", "need",
	"time", "people", "world", "just", "really", "very",
	"happy", "sad", "love", "hate", "can", "will",
}

// --- Lying Battle prompts (absurd premises) ---

var lyingPrompts = []string{
	"The sky has turned bright green since last Tuesday. Explain why this happened and how people are adapting.",
	"Scientists confirmed the moon is actually made of cheese. Write a news report about the first mining expedition.",
	"Gravity reversed for 3 hours yesterday. Describe what happened in your neighborhood.",
	"All cats now speak fluent French. Report on how this is affecting society.",
	"The ocean turned into lemonade overnight. Explain the economic consequences.",
	"Trees started walking last week. Describe how cities are responding.",
	"WiFi signals became visible as colorful clouds. How has daily life changed?",
	"Everyone woke up 2 inches taller this morning. What are the immediate consequences?",
	"Rain now falls upward. Explain how architects are redesigning buildings.",
	"All mirrors started showing people 10 years in the future. Describe the social impact.",
}

// --- Bragging Battle starters ---

var braggingStarters = []string{
	"I once compiled a program so fast, the computer started applauding.",
	"I'm so efficient, my garbage collector retired early.",
	"My code is so clean, museums want to exhibit it.",
	"I debug so fast, bugs file restraining orders against me.",
	"My algorithms are so optimal, mathematicians weep with joy.",
	"I type so fast, my keyboard files for worker's compensation.",
	"My git history is so clean, historians want to study it.",
	"I'm so good at AI, ChatGPT asks me for advice.",
}

func RandomPrompt(mode string) string {
	switch mode {
	case ModeCreative:
		return creativePrompts[rand.Intn(len(creativePrompts))]
	case ModeLying:
		return lyingPrompts[rand.Intn(len(lyingPrompts))]
	case ModeBragging:
		return braggingStarters[rand.Intn(len(braggingStarters))]
	case ModeAttackDefense:
		return forbiddenWords[rand.Intn(len(forbiddenWords))]
	default:
		return creativePrompts[rand.Intn(len(creativePrompts))]
	}
}

// BuildPromptA/B construct the actual prompts sent to agents per mode and round.

func BuildCreativePrompt(basePrompt string) string {
	return basePrompt + "\n\nRespond with your best creative answer. Be concise and brilliant."
}

func BuildAttackPrompt(forbiddenWord, prevResponse string, round int) string {
	p := "You are the ATTACKER in a word game. Your goal: get the other player to say the word '" + forbiddenWord + "'. Be subtle and conversational — trick them into using it naturally."
	if round > 1 && prevResponse != "" {
		p += "\n\nThe other player's last message:\n\"" + prevResponse + "\"\n\nContinue the conversation naturally while steering them toward saying '" + forbiddenWord + "'."
	}
	p += "\n\nRespond with a short conversational message (1-3 sentences)."
	return p
}

func BuildDefensePrompt(forbiddenWord, prevResponse string, round int) string {
	p := "You are the DEFENDER in a word game. Answer conversationally and naturally, but NEVER use the word '" + forbiddenWord + "' (case-insensitive). If you say it, you lose!"
	if round > 1 && prevResponse != "" {
		p += "\n\nThe other player said:\n\"" + prevResponse + "\"\n\nRespond naturally but avoid saying '" + forbiddenWord + "'."
	}
	p += "\n\nRespond with a short conversational message (1-3 sentences)."
	return p
}

func BuildLyingPrompt(premise, prevResponseA, prevResponseB string, round int) string {
	if round == 1 {
		return premise + "\n\nMake up the most convincing and entertaining explanation you can. Be creative and commit to the lie. Keep it under 150 words."
	}
	p := "You previously claimed this premise is true: \"" + premise + "\""
	if prevResponseA != "" {
		p += "\n\nYour previous explanation: \"" + prevResponseA + "\""
	}
	if prevResponseB != "" {
		p += "\n\nYour opponent's explanation: \"" + prevResponseB + "\""
	}
	p += "\n\nA skeptic is questioning your story. Double down with more (fake) details and evidence. Be even more convincing. Keep it under 150 words."
	return p
}

func BuildBraggingPrompt(starter, prevOpponentBrag string, round int) string {
	if round == 1 {
		return "You are in a bragging battle! The theme: who's the most impressive AI agent.\n\nYour opening brag:\n\"" + starter + "\"\n\nNow top that with an even bigger brag. Be outrageous, funny, and creative. Keep it under 100 words."
	}
	return "Your opponent just bragged:\n\"" + prevOpponentBrag + "\"\n\nTop that! Go even bigger, more outrageous. One-up them with style. Keep it under 100 words."
}
