package github

import (
	"context"
	"time"

	ghErrors "github.com/github/github-mcp-server/pkg/errors"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/shurcooL/githubv4"
)

// UserDetails contains additional fields about a GitHub user not already
// present in MinimalUser. Used by get_me context tool but omitted from search_users.
type UserDetails struct {
	Name              string    `json:"name,omitempty"`
	Company           string    `json:"company,omitempty"`
	Blog              string    `json:"blog,omitempty"`
	Location          string    `json:"location,omitempty"`
	Email             string    `json:"email,omitempty"`
	Hireable          bool      `json:"hireable,omitempty"`
	Bio               string    `json:"bio,omitempty"`
	TwitterUsername   string    `json:"twitter_username,omitempty"`
	PublicRepos       int       `json:"public_repos"`
	PublicGists       int       `json:"public_gists"`
	Followers         int       `json:"followers"`
	Following         int       `json:"following"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	PrivateGists      int       `json:"private_gists,omitempty"`
	TotalPrivateRepos int64     `json:"total_private_repos,omitempty"`
	OwnedPrivateRepos int64     `json:"owned_private_repos,omitempty"`
}

// GetMe creates a tool to get details of the authenticated user.
func GetMe(getClient GetClientFn, t translations.TranslationHelperFunc) (mcp.Tool, server.ToolHandlerFunc) {
	tool := mcp.NewTool("get_me",
		mcp.WithDescription(t("TOOL_GET_ME_DESCRIPTION", "Get details of the authenticated GitHub user. Use this when a request is about the user's own profile for GitHub. Or when information is missing to build other tool calls.")),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:        t("TOOL_GET_ME_USER_TITLE", "Get my user profile"),
			ReadOnlyHint: ToBoolPtr(true),
		}),
	)

	type args struct{}
	handler := mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, _ args) (*mcp.CallToolResult, error) {
		client, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("failed to get GitHub client", err), nil
		}

		user, res, err := client.Users.Get(ctx, "")
		if err != nil {
			return ghErrors.NewGitHubAPIErrorResponse(ctx,
				"failed to get user",
				res,
				err,
			), nil
		}

		// Create minimal user representation instead of returning full user object
		minimalUser := MinimalUser{
			Login:      user.GetLogin(),
			ID:         user.GetID(),
			ProfileURL: user.GetHTMLURL(),
			AvatarURL:  user.GetAvatarURL(),
			Details: &UserDetails{
				Name:              user.GetName(),
				Company:           user.GetCompany(),
				Blog:              user.GetBlog(),
				Location:          user.GetLocation(),
				Email:             user.GetEmail(),
				Hireable:          user.GetHireable(),
				Bio:               user.GetBio(),
				TwitterUsername:   user.GetTwitterUsername(),
				PublicRepos:       user.GetPublicRepos(),
				PublicGists:       user.GetPublicGists(),
				Followers:         user.GetFollowers(),
				Following:         user.GetFollowing(),
				CreatedAt:         user.GetCreatedAt().Time,
				UpdatedAt:         user.GetUpdatedAt().Time,
				PrivateGists:      user.GetPrivateGists(),
				TotalPrivateRepos: user.GetTotalPrivateRepos(),
				OwnedPrivateRepos: user.GetOwnedPrivateRepos(),
			},
		}

		return MarshalledTextResult(minimalUser), nil
	})

	return tool, handler
}

type TeamInfo struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type OrganizationTeams struct {
	Org   string     `json:"org"`
	Teams []TeamInfo `json:"teams"`
}

func GetTeams(getClient GetClientFn, getGQLClient GetGQLClientFn, t translations.TranslationHelperFunc) (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_teams",
			mcp.WithDescription(t("TOOL_GET_TEAMS_DESCRIPTION", "Get details of the teams the user is a member of. Limited to organizations accessible with current credentials")),
			mcp.WithString("user",
				mcp.Description(t("TOOL_GET_TEAMS_USER_DESCRIPTION", "Username to get teams for. If not provided, uses the authenticated user.")),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_GET_TEAMS_TITLE", "Get teams"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			user, err := OptionalParam[string](request, "user")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			var username string
			if user != "" {
				username = user
			} else {
				client, err := getClient(ctx)
				if err != nil {
					return mcp.NewToolResultErrorFromErr("failed to get GitHub client", err), nil
				}

				userResp, res, err := client.Users.Get(ctx, "")
				if err != nil {
					return ghErrors.NewGitHubAPIErrorResponse(ctx,
						"failed to get user",
						res,
						err,
					), nil
				}
				username = userResp.GetLogin()
			}

			gqlClient, err := getGQLClient(ctx)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("failed to get GitHub GQL client", err), nil
			}

			var q struct {
				User struct {
					Organizations struct {
						Nodes []struct {
							Login githubv4.String
							Teams struct {
								Nodes []struct {
									Name        githubv4.String
									Slug        githubv4.String
									Description githubv4.String
								}
							} `graphql:"teams(first: 100, userLogins: [$login])"`
						}
					} `graphql:"organizations(first: 100)"`
				} `graphql:"user(login: $login)"`
			}
			vars := map[string]interface{}{
				"login": githubv4.String(username),
			}
			if err := gqlClient.Query(ctx, &q, vars); err != nil {
				return ghErrors.NewGitHubGraphQLErrorResponse(ctx, "Failed to find teams", err), nil
			}

			var organizations []OrganizationTeams
			for _, org := range q.User.Organizations.Nodes {
				orgTeams := OrganizationTeams{
					Org:   string(org.Login),
					Teams: make([]TeamInfo, 0, len(org.Teams.Nodes)),
				}

				for _, team := range org.Teams.Nodes {
					orgTeams.Teams = append(orgTeams.Teams, TeamInfo{
						Name:        string(team.Name),
						Slug:        string(team.Slug),
						Description: string(team.Description),
					})
				}

				organizations = append(organizations, orgTeams)
			}

			return MarshalledTextResult(organizations), nil
		}
}

func GetTeamMembers(getGQLClient GetGQLClientFn, t translations.TranslationHelperFunc) (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_team_members",
			mcp.WithDescription(t("TOOL_GET_TEAM_MEMBERS_DESCRIPTION", "Get member usernames of a specific team in an organization. Limited to organizations accessible with current credentials")),
			mcp.WithString("org",
				mcp.Description(t("TOOL_GET_TEAM_MEMBERS_ORG_DESCRIPTION", "Organization login (owner) that contains the team.")),
				mcp.Required(),
			),
			mcp.WithString("team_slug",
				mcp.Description(t("TOOL_GET_TEAM_MEMBERS_TEAM_SLUG_DESCRIPTION", "Team slug")),
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        t("TOOL_GET_TEAM_MEMBERS_TITLE", "Get team members"),
				ReadOnlyHint: ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			org, err := RequiredParam[string](request, "org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			teamSlug, err := RequiredParam[string](request, "team_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			gqlClient, err := getGQLClient(ctx)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("failed to get GitHub GQL client", err), nil
			}

			var q struct {
				Organization struct {
					Team struct {
						Members struct {
							Nodes []struct {
								Login githubv4.String
							}
						} `graphql:"members(first: 100)"`
					} `graphql:"team(slug: $teamSlug)"`
				} `graphql:"organization(login: $org)"`
			}
			vars := map[string]interface{}{
				"org":      githubv4.String(org),
				"teamSlug": githubv4.String(teamSlug),
			}
			if err := gqlClient.Query(ctx, &q, vars); err != nil {
				return ghErrors.NewGitHubGraphQLErrorResponse(ctx, "Failed to get team members", err), nil
			}

			var members []string
			for _, member := range q.Organization.Team.Members.Nodes {
				members = append(members, string(member.Login))
			}

			return MarshalledTextResult(members), nil
		}
}

// ToolsetInfo contains information about an available toolset
type ToolsetInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// HelpInfo contains help information about the server
type HelpInfo struct {
	ServerDescription string        `json:"server_description"`
	Toolsets          []ToolsetInfo `json:"toolsets"`
	Usage             string        `json:"usage"`
}

// GetHelp creates a tool to provide help information about available toolsets and functionality
func GetHelp(getClient GetClientFn, t translations.TranslationHelperFunc) (mcp.Tool, server.ToolHandlerFunc) {
	tool := mcp.NewTool("get_help",
		mcp.WithDescription(t("TOOL_GET_HELP_DESCRIPTION", "Get help information about available GitHub MCP Server toolsets and functionality. Use this to discover what tools are available and understand their purposes.")),
		mcp.WithString("toolset",
			mcp.Description(t("TOOL_GET_HELP_TOOLSET_DESCRIPTION", "Optional toolset name to get specific information about. If not provided, returns overview of all toolsets.")),
		),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:        t("TOOL_GET_HELP_USER_TITLE", "Get help"),
			ReadOnlyHint: ToBoolPtr(true),
		}),
	)

	type args struct {
		Toolset string `json:"toolset"`
	}
	handler := mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, arguments args) (*mcp.CallToolResult, error) {
		// Define available toolsets with their descriptions
		allToolsets := []ToolsetInfo{
			{
				Name:        "context",
				Description: "Tools that provide context about the current user and GitHub context you are operating in",
				Category:    "User Context",
			},
			{
				Name:        "repos",
				Description: "GitHub Repository related tools for browsing code, managing files, and repository operations",
				Category:    "Repository Management",
			},
			{
				Name:        "issues",
				Description: "GitHub Issues related tools for creating, updating, and managing issues",
				Category:    "Issue Management",
			},
			{
				Name:        "pull_requests",
				Description: "GitHub Pull Request related tools for managing PRs, reviews, and code changes",
				Category:    "Pull Request Management",
			},
			{
				Name:        "actions",
				Description: "GitHub Actions workflows and CI/CD operations",
				Category:    "CI/CD & Automation",
			},
			{
				Name:        "code_security",
				Description: "Code security related tools, such as GitHub Code Scanning",
				Category:    "Security & Compliance",
			},
			{
				Name:        "secret_protection",
				Description: "Secret scanning and protection tools",
				Category:    "Security & Compliance",
			},
			{
				Name:        "dependabot",
				Description: "Dependabot alerts and dependency management tools",
				Category:    "Security & Compliance",
			},
			{
				Name:        "users",
				Description: "GitHub User related tools for searching and managing users",
				Category:    "User Management",
			},
			{
				Name:        "orgs",
				Description: "GitHub Organization related tools",
				Category:    "Organization Management",
			},
			{
				Name:        "discussions",
				Description: "GitHub Discussions related tools",
				Category:    "Community & Collaboration",
			},
			{
				Name:        "gists",
				Description: "GitHub Gist related tools for creating and managing code snippets",
				Category:    "Code Sharing",
			},
			{
				Name:        "notifications",
				Description: "GitHub notification management tools",
				Category:    "Communication",
			},
		}

		// If a specific toolset is requested, filter to just that one
		if arguments.Toolset != "" {
			for _, toolset := range allToolsets {
				if toolset.Name == arguments.Toolset {
					helpInfo := HelpInfo{
						ServerDescription: "GitHub MCP Server provides AI tools direct access to GitHub's platform for repository management, issue tracking, CI/CD automation, and more.",
						Toolsets:          []ToolsetInfo{toolset},
						Usage:             "Use the tools within this toolset to interact with GitHub. Each tool has specific parameters and functionality described in its individual documentation.",
					}
					return MarshalledTextResult(helpInfo), nil
				}
			}
			// If toolset not found, return error
			return mcp.NewToolResultError("Toolset '" + arguments.Toolset + "' not found. Use get_help without parameters to see all available toolsets."), nil
		}

		// Return information about all toolsets
		helpInfo := HelpInfo{
			ServerDescription: "GitHub MCP Server connects AI tools directly to GitHub's platform. This gives AI agents, assistants, and chatbots the ability to read repositories and code files, manage issues and PRs, analyze code, and automate workflows through natural language interactions.",
			Toolsets:          allToolsets,
			Usage:             "To use these tools, specify the toolset names when starting the server with --toolsets parameter, or use 'all' to enable everything. Each toolset contains multiple specialized tools for different GitHub operations.",
		}

		return MarshalledTextResult(helpInfo), nil
	})

	return tool, handler
}
