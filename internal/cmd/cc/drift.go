package cc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws-cloudformation/rain/cft"
	"github.com/aws-cloudformation/rain/cft/diff"
	"github.com/aws-cloudformation/rain/cft/parse"
	"github.com/aws-cloudformation/rain/internal/aws/ccapi"
	"github.com/aws-cloudformation/rain/internal/aws/s3"
	"github.com/aws-cloudformation/rain/internal/config"
	"github.com/aws-cloudformation/rain/internal/console"
	"github.com/aws-cloudformation/rain/internal/console/spinner"
	"github.com/aws-cloudformation/rain/internal/s11n"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func runDrift(cmd *cobra.Command, args []string) {

	name := args[0]

	if !Experimental {
		panic("Please add the --experimental arg to use this feature")
	}

	spinner.Push("Downloading state file")

	bucketName := s3.RainBucket(true)

	key := fmt.Sprintf("%v/%v.yaml", STATE_DIR, name) // deployments/name

	obj, err := s3.GetObject(bucketName, key)
	if err != nil {
		panic(fmt.Errorf("Unable to download state: %v", err))
	}

	config.Debugf("State file: %s", obj)

	template, err := parse.String(string(obj))
	if err != nil {
		panic(err)
	}

	resources, err := template.GetSection(cft.Resources)
	if err != nil {
		panic(err)
	}

	_, err = template.GetSection(cft.State)
	if err != nil {
		panic(err)
	}

	spinner.Pop()

	// Display deployment meta-data

	fmt.Println()
	fmt.Print(console.Blue("Deployment name:  "))
	fmt.Print(console.Cyan(fmt.Sprintf("%s\n", name)))

	fmt.Print(console.Blue("State file:       "))
	fmt.Print(console.Cyan(fmt.Sprintf("s3://%s/%s\n", bucketName, key)))

	localPath, err := template.GetNode(cft.State, "FilePath")
	if err != nil {
		panic(err)
	}
	fmt.Print(console.Blue("Local path:       "))
	fmt.Print(console.Cyan(fmt.Sprintf("%s\n", localPath.Value)))

	lastWrite, err := template.GetNode(cft.State, "LastWriteTime")
	if err != nil {
		panic(err)
	}
	fmt.Print(console.Blue("Last write time:  "))
	fmt.Print(console.Cyan(fmt.Sprintf("%s\n", lastWrite.Value)))

	resourceModels, err := template.GetNode(cft.State, "ResourceModels")
	if err != nil {
		panic(err)
	}

	fmt.Println()

	// Query each resource and stop to ask how to handle drift after each one
	for i := 0; i < len(resources.Content); i += 2 {
		resourceName := resources.Content[i].Value
		resourceNode := resources.Content[i+1]
		_, resourceModel := s11n.GetMapValue(resourceModels, resourceName)
		if resourceModel == nil {
			panic(fmt.Errorf("expected %s to have a ResourceModel", resourceName))
		}
		err := handleDrift(resourceName, resourceNode, resourceModel)
		if err != nil {
			panic(err)
		}
	}

}

func handleDrift(resourceName string, resourceNode *yaml.Node, model *yaml.Node) error {

	_, t := s11n.GetMapValue(resourceNode, "Type")
	if t == nil {
		return fmt.Errorf("resource %s expected to have Type", resourceName)
	}
	_, id := s11n.GetMapValue(model, "Identifier")
	if id == nil {
		return fmt.Errorf("resource model %s expected to have Identifier", resourceName)
	}
	title := fmt.Sprintf("%s (%s %s)", resourceName, t.Value, id.Value)

	spinner.Push(fmt.Sprintf("Querying CCAPI: %s", title))

	liveModelString, err := ccapi.GetResource(id.Value, t.Value)
	if err != nil {
		return err
	}
	spinner.Pop()

	_, stateModel := s11n.GetMapValue(model, "Model")
	if stateModel == nil {
		return fmt.Errorf("expected State %s to have Model", resourceName)
	}

	//modelString, _ := json.MarshalIndent(format.Jsonise(stateModel), "", "    ")

	var liveModelMap map[string]any
	err = json.Unmarshal([]byte(liveModelString), &liveModelMap)
	if err != nil {
		return err
	}

	var modelMap map[string]any
	err = stateModel.Decode(&modelMap)
	if err != nil {
		panic(err)
	}

	d := diff.CompareMaps(modelMap, liveModelMap)

	if d.Mode() == diff.Unchanged {
		fmt.Println(console.Green(title + "... Ok!"))
	} else {
		fmt.Println(console.Red(title + "... Drift detected!"))
		fmt.Println()
		fmt.Println("Live state")
		fmt.Println(colorDiff(d.Format(true)))
		reverse := diff.CompareMaps(liveModelMap, modelMap)
		fmt.Println("Stored state")
		fmt.Println(colorDiff(reverse.Format(true)))
	}

	// TODO
	//
	// What would you like to do with this resource?
	//
	// 1. Fix the live state so it matches the state file
	// 2. Apply the live state to the state file
	// 3. Do nothing
	//
	// [Continue asking about each resource, remembering answers]
	// [Confirm the changes that will be made using the normal deployment flow]

	fmt.Println()
	return nil
}

// colorDiff hacks the diff output to colorize it
func colorDiff(s string) string {
	lines := strings.Split(s, "\n")
	f := "%s "
	//changed := []string{diff.Added, diff.Removed, diff.Changed, diff.Involved}
	unchanged := fmt.Sprintf(f, diff.Unchanged)
	ret := make([]string, 0)
	for _, line := range lines {
		// Lines look like these:
		// (=) QueryDefinitionId: 0abf4544-b551-4b79-93d0-6f7f294cdbaa
		// (>) QueryString: fields @message, @timestamp
		tokens := strings.SplitAfterN(line, " ", 2)
		if len(tokens) != 2 {
			ret = append(ret, console.Yellow(line)) // Shouldn't happen
		} else {
			if tokens[0] == unchanged {
				ret = append(ret, console.Green(tokens[1]))
			} else {
				ret = append(ret, console.Red(tokens[1]))
			}
		}
	}
	return strings.Join(ret, "\n")
}

var CCDriftCmd = &cobra.Command{
	Use:   "drift <name>",
	Short: "Compare the state file to the live state of the resources",
	Long: `When deploying templates with the cc command, a state file is created and stored in the rain assets bucket. This command outputs a diff of that file and the actual state of the resources, according to Cloud Control API. You can then apply the changes by changing the live state, or by modifying the state file.
`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run:                   runDrift,
}

func init() {
	CCDriftCmd.Flags().BoolVar(&config.Debug, "debug", false, "Output debugging information")
	CCDriftCmd.Flags().BoolVarP(&Experimental, "experimental", "x", false, "Acknowledge that this is an experimental feature")
}
