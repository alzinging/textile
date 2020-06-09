package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/logrusorgru/aurora"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"github.com/textileio/go-threads/core/thread"
	"github.com/textileio/go-threads/db"
	"github.com/textileio/textile/api/common"
	bucks "github.com/textileio/textile/buckets"
	"github.com/textileio/textile/buckets/local"
	"github.com/textileio/textile/cmd"
)

type bucketInfo struct {
	ID   thread.ID
	Name string
	Key  string
}

var bucketInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new or existing bucket",
	Long: `Initializes a new or existing bucket.

A .textile config directory and a seed file will be created in the current working directory.
Existing configs will not be overwritten.

Use the '--existing' flag to initialize from an existing remote bucket.
`,
	Args: cobra.ExactArgs(0),
	PreRun: func(c *cobra.Command, args []string) {
		cmd.ExpandConfigVars(config.Viper, config.Flags)
	},
	Run: func(c *cobra.Command, args []string) {
		root, err := os.Getwd()
		if err != nil {
			cmd.Fatal(err)
		}
		dir := filepath.Join(root, config.Dir)
		if err = os.MkdirAll(dir, os.ModePerm); err != nil {
			cmd.Fatal(err)
		}
		filename := filepath.Join(dir, config.Name+".yml")
		if _, err := os.Stat(filename); err == nil {
			cmd.Fatal(fmt.Errorf("bucket %s is already initialized", root))
		}

		pass := config.Viper.GetString("password")

		existing, err := c.Flags().GetBool("existing")
		if err != nil {
			cmd.Fatal(err)
		}
		if existing {
			threads := clients.ListThreads(true)
			bi := make([]bucketInfo, 0)
			ctx, cancel := clients.Ctx.Auth(cmd.Timeout)
			defer cancel()
			for _, t := range threads {
				ctx = common.NewThreadIDContext(ctx, t.ID)
				res, err := clients.Buckets.List(ctx)
				if err != nil {
					cmd.Fatal(err)
				}
				for _, root := range res.Roots {
					name := "unnamed"
					if root.Name != "" {
						name = root.Name
					}
					bi = append(bi, bucketInfo{ID: t.ID, Name: name, Key: root.Key})
				}
			}

			prompt := promptui.Select{
				Label: "Which exiting bucket do you want to init from?",
				Items: bi,
				Templates: &promptui.SelectTemplates{
					Active:   fmt.Sprintf(`{{ "%s" | cyan }} {{ .Name | bold }} {{ .Key | faint | bold }}`, promptui.IconSelect),
					Inactive: `{{ .Name | faint }} {{ .Key | faint | bold }}`,
					Selected: aurora.Sprintf(aurora.BrightBlack("> Selected bucket {{ .Name | white | bold }}")),
				},
			}
			index, _, err := prompt.Run()
			if err != nil {
				cmd.Fatal(err)
			}

			selected := bi[index]
			config.Viper.Set("thread", selected.ID.String())
			config.Viper.Set("key", selected.Key)

			if pass == "" {
				passp := promptui.Prompt{
					Label: fmt.Sprintf("Enter the encryption password for %s (optional)", selected.Name),
					Mask:  '*',
				}
				pass, err = passp.Run()
				if err != nil {
					cmd.End("")
				}
			}
		}

		var dbID thread.ID
		xthread := config.Viper.GetString("thread")
		if xthread != "" {
			var err error
			dbID, err = thread.Decode(xthread)
			if err != nil {
				cmd.Fatal(fmt.Errorf("invalid thread ID"))
			}
		}

		xkey := config.Viper.GetString("key")
		initRemote := true
		if xkey != "" {
			if !dbID.Defined() {
				cmd.Fatal(fmt.Errorf("the --thread flag is required when using --key"))
			}
			initRemote = false
		}

		var name string
		if initRemote {
			namep := promptui.Prompt{
				Label: "Enter a name for your new bucket (optional)",
			}
			var err error
			name, err = namep.Run()
			if err != nil {
				cmd.End("")
			}
			if pass == "" {
				passp := promptui.Prompt{
					Label: "Enter an encryption password for your new bucket (optional)",
					Mask:  '*',
				}
				pass, err = passp.Run()
				if err != nil {
					cmd.End("")
				}
			}
		}

		if pass != "" {
			config.Viper.Set("password", pass)
		}

		if !dbID.Defined() {
			selected := clients.SelectThread("Buckets are written to a threadDB. Select or create a new one", aurora.Sprintf(
				aurora.BrightBlack("> Selected threadDB {{ .Label | white | bold }}")), true)
			if selected.Label == "Create new" {
				if selected.Name == "" {
					prompt := promptui.Prompt{
						Label: "Enter a name for your new threadDB (optional)",
					}
					var err error
					selected.Name, err = prompt.Run()
					if err != nil {
						cmd.End("")
					}
				}
				ctx, cancel := clients.Ctx.Auth(cmd.Timeout)
				defer cancel()
				ctx = common.NewThreadNameContext(ctx, selected.Name)
				dbID = thread.NewIDV1(thread.Raw, 32)
				if err := clients.Threads.NewDB(ctx, dbID, db.WithNewManagedName(selected.Name)); err != nil {
					cmd.Fatal(err)
				}
			} else {
				dbID = selected.ID
			}
			config.Viper.Set("thread", dbID.String())
		}

		if initRemote {
			ctx, cancel := clients.Ctx.Thread(cmd.Timeout)
			defer cancel()
			rep, err := clients.Buckets.Init(ctx, name)
			if err != nil {
				cmd.Fatal(err)
			}
			config.Viper.Set("key", rep.Root.Key)

			seed := filepath.Join(root, bucks.SeedName)
			file, err := os.Create(seed)
			if err != nil {
				cmd.Fatal(err)
			}
			_, err = file.Write(rep.Seed)
			if err != nil {
				file.Close()
				cmd.Fatal(err)
			}
			file.Close()

			buck, err := local.NewBucket(root, options.BalancedLayout)
			if err != nil {
				cmd.Fatal(err)
			}
			actx, acancel := context.WithTimeout(context.Background(), cmd.Timeout)
			defer acancel()
			if err = buck.SaveFile(actx, seed, bucks.SeedName); err != nil {
				cmd.Fatal(err)
			}
			// We just have the seed file, which is never encrypted.
			// So, the remote root will be equal to the local root.
			if err = buck.SetRemote(buck.Local()); err != nil {
				cmd.Fatal(err)
			}

			printLinks(rep.Links)
		}

		if err := config.Viper.WriteConfigAs(filename); err != nil {
			cmd.Fatal(err)
		}
		if initRemote {
			cmd.Success("Initialized an empty bucket in %s", aurora.White(root).Bold())
		} else {
			key := config.Viper.GetString("key")
			count := getPath(key, "", root, nil, nil, false)

			buck, err := local.NewBucket(root, options.BalancedLayout)
			if err != nil {
				cmd.Fatal(err)
			}
			rr := getRemoteRoot(key)
			if err := buck.SetRemote(rr); err != nil {
				cmd.Fatal(err)
			}
			buck.SetCidVersion(int(rr.Version()))
			ctx, cancel := context.WithTimeout(context.Background(), cmd.Timeout)
			defer cancel()
			if err = buck.Save(ctx); err != nil {
				cmd.Fatal(err)
			}
			cmd.Success("Initialized from remote and pulled %d objects to %s", aurora.White(count).Bold(), aurora.White(root).Bold())
		}
	},
}
