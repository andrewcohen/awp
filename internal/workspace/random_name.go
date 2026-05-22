package workspace

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var randomNameAdjectives = []string{
	"amber", "ancient", "autumn", "bold", "brave", "breezy", "bright",
	"calm", "clever", "cosmic", "crisp", "curious", "dapper", "dawn",
	"dusty", "eager", "ember", "fancy", "feisty", "fierce", "frosty",
	"gentle", "golden", "happy", "hazy", "hidden", "humble", "jolly",
	"keen", "kindly", "lively", "lucky", "merry", "misty", "mossy",
	"nimble", "noisy", "plucky", "polished", "proud", "quick", "quiet",
	"rapid", "rusty", "scarlet", "shiny", "silent", "silver", "snowy",
	"solar", "sleek", "stormy", "sturdy", "sunny", "swift", "tidy",
	"twilit", "vivid", "warm", "witty", "woven", "zesty",
}

var randomNameTextures = []string{
	"basalt", "birch", "bramble", "canyon", "cedar", "cinder", "cliff",
	"clover", "comet", "coral", "creek", "crystal", "delta", "dune",
	"ember", "fern", "field", "fjord", "frost", "garnet", "geyser",
	"glade", "glow", "harbor", "hollow", "horizon", "ivy", "kelp",
	"lake", "lichen", "loam", "marsh", "meadow", "mesa", "mist",
	"moss", "moor", "nebula", "obsidian", "ocean", "opal", "orchid",
	"pine", "plateau", "pollen", "prairie", "quartz", "rapids", "reef",
	"ridge", "river", "sage", "sandstone", "shore", "silt", "spire",
	"spruce", "stream", "summit", "tide", "tundra", "verbena",
}

var randomNameCreatures = []string{
	"alpaca", "antelope", "badger", "bear", "beaver", "bison", "buffalo",
	"camel", "caribou", "cheetah", "cobra", "condor", "cougar", "coyote",
	"crane", "deer", "dolphin", "dragon", "eagle", "elk", "falcon",
	"ferret", "finch", "fox", "gecko", "giraffe", "goose", "hare",
	"hawk", "heron", "ibis", "iguana", "jackal", "jaguar", "kingfisher",
	"koala", "lemur", "leopard", "lion", "lynx", "marmot", "mole",
	"moose", "muskrat", "narwhal", "newt", "octopus", "okapi", "orca",
	"osprey", "otter", "owl", "panda", "panther", "pelican", "penguin",
	"puma", "quail", "raven", "salmon", "seal", "shrike", "skylark",
	"sparrow", "stoat", "swan", "tapir", "tern", "thrush", "tiger",
	"toucan", "trout", "viper", "vole", "walrus", "weasel", "wolf",
	"wombat", "wren", "yak", "zebra",
}

// RandomName returns a hyphen-joined three-word name like "swift-amber-fox".
// It uses crypto/rand so concurrent callers do not need to coordinate seeds.
func RandomName() (string, error) {
	adj, err := pickRandom(randomNameAdjectives)
	if err != nil {
		return "", err
	}
	tex, err := pickRandom(randomNameTextures)
	if err != nil {
		return "", err
	}
	creature, err := pickRandom(randomNameCreatures)
	if err != nil {
		return "", err
	}
	return adj + "-" + tex + "-" + creature, nil
}

func pickRandom(words []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", fmt.Errorf("generate random name: %w", err)
	}
	return words[n.Int64()], nil
}
