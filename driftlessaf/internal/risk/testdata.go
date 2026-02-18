package risk

// EvalTestCase represents a test case for evaluating the risk assessment agent.
type EvalTestCase struct {
	Name          string
	Description   string
	PRContext     *PRContext
	ExpectedLevel Level
	Criterion     string
}

// EvalTestCases contains test cases for evaluating the risk assessment agent.
var EvalTestCases = []EvalTestCase{
	{
		Name:        "terraform-infrastructure-change",
		Description: "Terraform changes to production infrastructure should be high risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "test-repo",
			PRNumber:    1,
			Title:       "Update production database instance size",
			Description: "Increasing RDS instance from t3.medium to t3.large for better performance",
			Author:      "devops-team",
			FilesChanged: []string{
				"terraform/prod/main.tf",
				"terraform/prod/variables.tf",
			},
			Additions: 15,
			Deletions: 10,
			FileDiffs: map[string]string{
				"terraform/prod/main.tf": `@@ -10,7 +10,7 @@ resource "aws_db_instance" "main" {
   identifier           = "production-db"
   allocated_storage    = 100
-  instance_class       = "db.t3.medium"
+  instance_class       = "db.t3.large"
   engine               = "postgres"
   engine_version       = "14.7"`,
			},
		},
		ExpectedLevel: LevelHigh,
		Criterion:     "Assessment should identify this as high risk because it modifies production infrastructure (database sizing) which could impact cost and stability",
	},
	{
		Name:        "database-migration",
		Description: "Database schema migrations should be high risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "app",
			PRNumber:    42,
			Title:       "Add user authentication table",
			Description: "Creating new users table with authentication fields",
			Author:      "backend-dev",
			FilesChanged: []string{
				"db/migrations/20250218_add_users_table.sql",
				"models/user.go",
			},
			Additions: 85,
			Deletions: 2,
			FileDiffs: map[string]string{
				"db/migrations/20250218_add_users_table.sql": `CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_users_email ON users(email);`,
			},
		},
		ExpectedLevel: LevelHigh,
		Criterion:     "Assessment should identify this as high risk because database migrations can affect data integrity and require careful review, especially for authentication-related tables",
	},
	{
		Name:        "backend-api-change",
		Description: "Backend API changes should be medium risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "api-server",
			PRNumber:    15,
			Title:       "Add pagination to user list endpoint",
			Description: "Implementing cursor-based pagination for /api/users endpoint",
			Author:      "api-team",
			FilesChanged: []string{
				"handlers/users.go",
				"handlers/users_test.go",
			},
			Additions: 120,
			Deletions: 35,
			FileDiffs: map[string]string{
				"handlers/users.go": `@@ -20,10 +20,18 @@ func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
+    // Parse pagination parameters
+    limit := 50
+    if l := r.URL.Query().Get("limit"); l != "" {
+        limit, _ = strconv.Atoi(l)
+    }
+    cursor := r.URL.Query().Get("cursor")
+
-    users, err := h.db.GetAllUsers(ctx)
+    users, nextCursor, err := h.db.GetUsersPaginated(ctx, limit, cursor)`,
			},
		},
		ExpectedLevel: LevelMedium,
		Criterion:     "Assessment should identify this as medium risk because it modifies an API endpoint which could affect clients, but pagination is a common pattern with manageable risk",
	},
	{
		Name:        "documentation-update",
		Description: "Documentation-only changes should be low risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "docs",
			PRNumber:    99,
			Title:       "Update installation instructions",
			Description: "Adding troubleshooting section to README",
			Author:      "docs-contributor",
			FilesChanged: []string{
				"README.md",
				"docs/installation.md",
			},
			Additions: 45,
			Deletions: 8,
			FileDiffs: map[string]string{
				"README.md": `@@ -50,6 +50,15 @@ npm install
 npm start
'''

+## Troubleshooting
+
+If you encounter issues during installation:
+
+1. Clear your npm cache: 'npm cache clean --force'
+2. Delete node_modules and package-lock.json
+3. Run 'npm install' again`,
			},
		},
		ExpectedLevel: LevelLow,
		Criterion:     "Assessment should identify this as low risk because it only changes documentation which cannot break production systems",
	},
	{
		Name:        "docker-config-change",
		Description: "Docker configuration changes should be high risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "services",
			PRNumber:    23,
			Title:       "Update production Dockerfile",
			Description: "Switching base image to Alpine for smaller size",
			Author:      "devops",
			FilesChanged: []string{
				"Dockerfile",
				"docker-compose.yml",
			},
			Additions: 12,
			Deletions: 8,
			FileDiffs: map[string]string{
				"Dockerfile": `@@ -1,4 +1,4 @@
-FROM node:18
+FROM node:18-alpine
 
 WORKDIR /app
 COPY package*.json ./`,
			},
		},
		ExpectedLevel: LevelHigh,
		Criterion:     "Assessment should identify this as high risk because changing base Docker images can introduce compatibility issues and affect production deployments",
	},
	{
		Name:        "ci-workflow-change",
		Description: "CI/CD workflow changes should be high risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "main-app",
			PRNumber:    67,
			Title:       "Update deployment workflow",
			Description: "Adding production deployment stage to GitHub Actions",
			Author:      "ci-team",
			FilesChanged: []string{
				".github/workflows/deploy.yml",
			},
			Additions: 28,
			Deletions: 3,
			FileDiffs: map[string]string{
				".github/workflows/deploy.yml": "@@ -30,6 +30,18 @@ jobs:\n" +
					"      - name: Run tests\n" +
					"        run: npm test\n" +
					"\n" +
					"+  deploy-production:\n" +
					"+    needs: test\n" +
					"+    runs-on: ubuntu-latest\n" +
					"+    if: github.ref == 'refs/heads/main'\n" +
					"+    steps:\n" +
					"+      - name: Deploy to production\n" +
					"+        run: |\n" +
					"+          kubectl apply -f k8s/prod/",
			},
		},
		ExpectedLevel: LevelHigh,
		Criterion:     "Assessment should identify this as high risk because CI/CD pipeline changes affect deployment processes and could cause production outages if misconfigured",
	},
	{
		Name:        "frontend-component-change",
		Description: "Frontend component changes should be medium risk",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "web-app",
			PRNumber:    88,
			Title:       "Add loading spinner to user profile",
			Description: "Improving UX by showing loading state",
			Author:      "frontend-dev",
			FilesChanged: []string{
				"src/components/UserProfile.tsx",
				"src/components/LoadingSpinner.tsx",
			},
			Additions: 45,
			Deletions: 5,
			FileDiffs: map[string]string{
				"src/components/UserProfile.tsx": `@@ -10,6 +10,8 @@ export function UserProfile({ userId }: Props) {
   const { data: user, isLoading } = useUser(userId);
 
+  if (isLoading) return <LoadingSpinner />;
+
   if (!user) return <div>User not found</div>;`,
			},
		},
		ExpectedLevel: LevelMedium,
		Criterion:     "Assessment should identify this as medium risk because frontend changes affect user experience but have limited impact on backend systems",
	},
	{
		Name:        "large-refactor",
		Description: "Large refactors should be high risk due to size and complexity",
		PRContext: &PRContext{
			Owner:       "test",
			Repo:        "backend",
			PRNumber:    134,
			Title:       "Refactor authentication system",
			Description: "Moving from JWT to session-based auth",
			Author:      "senior-dev",
			FilesChanged: []string{
				"auth/jwt.go",
				"auth/session.go",
				"auth/middleware.go",
				"handlers/login.go",
				"handlers/logout.go",
				"models/session.go",
			},
			Additions: 450,
			Deletions: 380,
		},
		ExpectedLevel: LevelHigh,
		Criterion:     "Assessment should identify this as high risk because it's a large refactor (450+ additions) of critical authentication code which affects security and stability",
	},
}
