package proxy

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/shlex"
	"gopkg.in/yaml.v3"
)

const DEFAULT_GROUP_ID = "(default)"

type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	Unlisted      bool     `yaml:"unlisted"`
	UseModelName  string   `yaml:"useModelName"`
	MessagePrefix string   `yaml:"message_prefix"`
	CachePrompt   *bool    `yaml:"cache_prompt"` // Use pointer to differentiate between unset and false
}

func (m *ModelConfig) SanitizedCommand() ([]string, error) {
	return SanitizeCommand(m.Cmd)
}

type GroupConfig struct {
	Swap       bool     `yaml:"swap"`
	Exclusive  bool     `yaml:"exclusive"`
	Persistent bool     `yaml:"persistent"`
	Members    []string `yaml:"members"`
}

// set default values for GroupConfig
func (c *GroupConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawGroupConfig GroupConfig
	defaults := rawGroupConfig{
		Swap:       true,
		Exclusive:  true,
		Persistent: false,
		Members:    []string{},
	}

	if err := unmarshal(&defaults); err != nil {
		return err
	}

	*c = GroupConfig(defaults)
	return nil
}

type Config struct {
	HealthCheckTimeout int                    `yaml:"healthCheckTimeout"`
	LogRequests        bool                   `yaml:"logRequests"`
	LogLevel           string                 `yaml:"logLevel"`
	Models             map[string]ModelConfig `yaml:"models"` /* key is model ID */
	Profiles           map[string][]string    `yaml:"profiles"`
	Groups             map[string]GroupConfig `yaml:"groups"` /* key is group ID */

	// map aliases to actual model IDs
	aliases map[string]string
}

func (c *Config) RealModelName(search string) (string, bool) {
	if _, found := c.Models[search]; found {
		return search, true
	} else if name, found := c.aliases[search]; found {
		return name, found
	} else {
		return "", false
	}
}

func (c *Config) FindConfig(modelName string) (ModelConfig, string, bool) {
	if realName, found := c.RealModelName(modelName); !found {
		return ModelConfig{}, "", false
	} else {
		return c.Models[realName], realName, true
	}
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, err
	}

	if config.HealthCheckTimeout < 15 {
		config.HealthCheckTimeout = 15
	}

	// Populate the aliases map
	config.aliases = make(map[string]string)
	for modelName, modelConfig := range config.Models {
		for _, alias := range modelConfig.Aliases {
			config.aliases[alias] = modelName
		}
	}

	config = AddDefaultGroupToConfig(config)
	// check that members are all unique in the groups
	memberUsage := make(map[string]string) // maps member to group it appears in
	for groupID, groupConfig := range config.Groups {
		prevSet := make(map[string]bool)
		for _, member := range groupConfig.Members {
			// Check for duplicates within this group
			if _, found := prevSet[member]; found {
				return Config{}, fmt.Errorf("duplicate model member %s found in group: %s", member, groupID)
			}
			prevSet[member] = true

			// Check if member is used in another group
			if existingGroup, exists := memberUsage[member]; exists {
				return Config{}, fmt.Errorf("model member %s is used in multiple groups: %s and %s", member, existingGroup, groupID)
			}
			memberUsage[member] = groupID
		}
	}

	return config, nil
}

// rewrites the yaml to include a default group with any orphaned models
func AddDefaultGroupToConfig(config Config) Config {

	if config.Groups == nil {
		config.Groups = make(map[string]GroupConfig)
	}

	defaultGroup := GroupConfig{
		Swap:      true,
		Exclusive: true,
		Members:   []string{},
	}
	// if groups is empty, create a default group and put
	// all models into it
	if len(config.Groups) == 0 {
		for modelName := range config.Models {
			defaultGroup.Members = append(defaultGroup.Members, modelName)
		}
	} else {
		// iterate over existing group members and add non-grouped models into the default group
		for modelName, _ := range config.Models {
			foundModel := false
		found:
			// search for the model in existing groups
			for _, groupConfig := range config.Groups {
				for _, member := range groupConfig.Members {
					if member == modelName {
						foundModel = true
						break found
					}
				}
			}

			if !foundModel {
				defaultGroup.Members = append(defaultGroup.Members, modelName)
			}
		}
	}

	sort.Strings(defaultGroup.Members) // make consistent ordering for testing
	config.Groups[DEFAULT_GROUP_ID] = defaultGroup

	return config
}

func SanitizeCommand(cmdStr string) ([]string, error) {
	// Remove trailing backslashes
	cmdStr = strings.ReplaceAll(cmdStr, "\\ \n", " ")
	cmdStr = strings.ReplaceAll(cmdStr, "\\\n", " ")

	// Split the command into arguments
	args, err := shlex.Split(cmdStr)
	if err != nil {
		return nil, err
	}

	// Ensure the command is not empty
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	return args, nil
}
