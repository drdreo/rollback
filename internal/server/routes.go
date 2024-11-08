package server

import (
    "bytes"
    "database/sql"
    "encoding/gob"
    "encoding/json"
    "errors"
    "fmt"
    "github.com/gin-contrib/sessions"
    "github.com/gin-contrib/sessions/cookie"
    "github.com/gin-gonic/gin"
    "log"
    "net/http"
    "net/url"
    "os"
    "time"

    _ "github.com/mattn/go-sqlite3"
)

var (
    clientID     = os.Getenv("CLIENT_ID")
    clientSecret = os.Getenv("CLIENT_SECRET")

    // Create a new cookie store with a random key
    store = cookie.NewStore([]byte("secret")) // TODO: secret is very secret
)

// Cache settings
const (
    cacheEnabled    = true
    cacheExpiration = 60 * time.Minute // Cache expires after 1 hour
)

var sqlDB *sql.DB

const GITHUB_API_ENDPOINT = "https://api.github.com"

func (s *Server) RegisterRoutes() http.Handler {
    r := gin.Default()
    gin.ForceConsoleColor()
    log.Println("registering gob types")
    gob.Register(GitHubUser{})
    db, err := initializeDatabase()
    if err != nil {
        panic("failed to init db")
    }
    sqlDB = db

    r.LoadHTMLGlob("templates/*")
    r.Use(sessions.Sessions("cookie_session", store))

    r.GET("/", s.HomeHandler)
    r.GET("/deployments", s.DeploymentsHandler)
    r.GET("/auth/callback", s.CallbackHandler)

    return r
}

func (s *Server) HomeHandler(c *gin.Context) {
    c.HTML(http.StatusOK, "index.tmpl", gin.H{
        "clientId": clientID,
    })
}

func (s *Server) DeploymentsHandler(c *gin.Context) {
    session := sessions.Default(c)

    token := session.Get("access_token").(string)
    user := session.Get("gh_user").(GitHubUser)
    log.Println(user)
    deployments, err := fetchDeploymentInfos(token, user.Login, "habi")
    if err != nil {
        log.Println(err.Error())
        c.AbortWithError(http.StatusInternalServerError, errors.New("failed to fetch deployments: "+err.Error()))
        return
    }
    c.HTML(http.StatusOK, "deployments.tmpl", gin.H{
        "deployments": deployments,
        "user":        user,
    })
}

func (s *Server) CallbackHandler(c *gin.Context) {
    code := c.Query("code")
    if code == "" {
        c.AbortWithError(http.StatusBadRequest, errors.New("no code in the request"))
        return
    }

    log.Printf("Successfully authorized! Got code %s\n", code)

    // Exchange the code for an access token
    tokenData, err := exchangeCode(code)
    if err != nil || tokenData["access_token"] == nil {
        c.AbortWithError(http.StatusInternalServerError, errors.New("failed to exchange code - "+err.Error()))
        return
    }
    log.Printf("Successfully exchanged code for access_token! Got %s\n", tokenData["access_token"])

    // Fetch user info
    token := tokenData["access_token"].(string)
    log.Println("Setting access token: " + token)

    session := sessions.Default(c)
    session.Set("access_token", token)

    user, err := getGitHubUser(token)
    if err != nil {
        log.Println(err)
    }
    session.Set("gh_user", &user)

    err = session.Save()
    if err != nil {
        log.Println(err)
    }

    c.Redirect(301, "/deployments")
}

// Exchange the authorization code for an access token
func exchangeCode(code string) (map[string]interface{}, error) {
    data := url.Values{}
    data.Set("client_id", clientID)
    data.Set("client_secret", clientSecret)
    data.Set("code", code)

    req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", nil)
    if err != nil {
        return nil, err
    }

    req.URL.RawQuery = data.Encode()
    req.Header.Set("Accept", "application/json")

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }

    defer resp.Body.Close()

    var result map[string]interface{}
    err = json.NewDecoder(resp.Body).Decode(&result)
    if err != nil {
        return nil, err
    }

    return result, nil
}

func fetchDeploymentInfos(token, owner, repo string) ([]DeploymentInfo, error) {
    // Check the cache
    var deploymentInfos []DeploymentInfo
    if cacheEnabled {
        deploymentInfos, err := getDeploymentInfosFromCache(owner, repo)
        if err == nil {
            return deploymentInfos, err
        }
    }

    // Fetch from the GitHub API
    pageLimit := 5
    currentPage := 1
    apiUrl := fmt.Sprintf("/repos/%s/%s/deployments?per_page=%d&page=%d", owner, repo, pageLimit, currentPage)
    resp, err := makeGitHubAPIRequest(token, "GET", apiUrl, nil)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var deployments []Deployment
    if err := json.NewDecoder(resp.Body).Decode(&deployments); err != nil {
        return nil, err
    }
    log.Println(deployments)

    // Fetch additional details for each deployment
    for _, deployment := range deployments {
        var deploymentWithStatus DeploymentInfo
        statusResp, err := makeRequest(token, "GET", deployment.StatusesURL, nil)
        if err != nil {
            return nil, err
        }
        defer statusResp.Body.Close()

        var statuses []DeploymentStatus
        if statusResp.StatusCode >= 400 {
            var errorResp map[string]interface{}
            json.NewDecoder(statusResp.Body).Decode(&errorResp)
            log.Printf("Error fetching status: %s - %s\n", statusResp.Status, errorResp["message"])
        } else if err := json.NewDecoder(statusResp.Body).Decode(&statuses); err != nil {
            log.Printf("Error parsing status: %s\n", err.Error())
        }

        if len(statuses) > 0 {
            deploymentWithStatus.Statuses = statuses
        }

        deploymentWithStatus.Deployment = deployment
        deploymentInfos = append(deploymentInfos, deploymentWithStatus)
    }

    // Cache the result
    if cacheEnabled {
        err = cacheDeployments(owner, repo, deploymentInfos)
        if err != nil {
            return nil, err
        }
    }

    return deploymentInfos, nil
}

func getGitHubUser(token string) (*GitHubUser, error) {
    resp, err := makeGitHubAPIRequest(token, "GET", "/user", nil)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var user GitHubUser
    if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
        return nil, err
    }

    return &user, nil
}

func makeRequest(token, method, requestUrl string, requestBody interface{}) (*http.Response, error) {
    log.Println("Requesting: " + requestUrl)

    var req *http.Request
    var err error
    if requestBody != nil {
        body, err := json.Marshal(requestBody)
        if err != nil {
            return nil, err
        }
        req, err = http.NewRequest(method, requestUrl, bytes.NewBuffer(body))
    } else {
        req, err = http.NewRequest(method, requestUrl, nil)
    }
    if err != nil {
        return nil, err
    }

    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

    client := &http.Client{}
    return client.Do(req)
}

func makeGitHubAPIRequest(token, method, endpoint string, requestBody interface{}) (*http.Response, error) {
    requestUrl := fmt.Sprintf("%s%s", GITHUB_API_ENDPOINT, endpoint)
    return makeRequest(token, method, requestUrl, requestBody)
}

func getDeploymentInfosFromCache(owner, repo string) ([]DeploymentInfo, error) {
    log.Print("Getting deployments from cache")
    stmt, err := sqlDB.Prepare("SELECT data, expiration FROM deployments WHERE owner = ? AND repo = ?")
    if err != nil {
        return nil, err
    }
    defer stmt.Close()

    var data []byte
    var expiration time.Time
    err = stmt.QueryRow(owner, repo).Scan(&data, &expiration)
    if err != nil {
        return nil, err
    }

    if time.Now().After(expiration) {
        return nil, fmt.Errorf("cache expired")
    }

    var deployments []DeploymentInfo
    err = json.Unmarshal(data, &deployments)
    if err != nil {
        return nil, err
    }

    log.Printf("Got %d deployments from cache\n", len(deployments))
    return deployments, nil
}

func cacheDeployments(user, repo string, deployments []DeploymentInfo) error {
    tx, err := sqlDB.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    stmt, err := tx.Prepare("INSERT OR REPLACE INTO deployments (owner, repo, data, expiration) VALUES (?, ?, ?, ?)")
    if err != nil {
        return err
    }
    defer stmt.Close()

    data, err := json.Marshal(deployments)
    if err != nil {
        return err
    }

    _, err = stmt.Exec(user, repo, data, time.Now().Add(cacheExpiration))
    if err != nil {
        return err
    }

    err = tx.Commit()
    if err != nil {
        return err
    }

    return nil
}

func initializeDatabase() (*sql.DB, error) {
    // Open or create the database
    db, err := sql.Open("sqlite3", "deployments.db")
    if err != nil {
        return nil, err
    }

    // Create the table if it doesn't already exist
    createTableQuery := `
    CREATE TABLE IF NOT EXISTS deployments (
        owner TEXT NOT NULL,
        repo TEXT NOT NULL,
        data TEXT,
        expiration TIMESTAMP,
        PRIMARY KEY (owner, repo)
    );
    `
    _, err = db.Exec(createTableQuery)
    if err != nil {
        db.Close()
        return nil, err
    }

    return db, nil
}

type DeploymentStatus struct {
    URL                   string            `json:"url"`
    ID                    int64             `json:"id"`
    NodeID                string            `json:"node_id"`
    State                 string            `json:"state"`
    Creator               DeploymentCreator `json:"creator"`
    Description           string            `json:"description"`
    Environment           string            `json:"environment"`
    TargetURL             string            `json:"target_url"`
    CreatedAt             time.Time         `json:"created_at"`
    UpdatedAt             time.Time         `json:"updated_at"`
    DeploymentURL         string            `json:"deployment_url"`
    RepositoryURL         string            `json:"repository_url"`
    EnvironmentURL        string            `json:"environment_url"`
    LogURL                string            `json:"log_url"`
    PerformedViaGithubApp any               `json:"performed_via_github_app"`
}

type GitHubUser struct {
    Login                   string    `json:"login"`
    ID                      int       `json:"id"`
    NodeID                  string    `json:"node_id"`
    AvatarURL               string    `json:"avatar_url"`
    GravatarID              string    `json:"gravatar_id"`
    URL                     string    `json:"url"`
    HTMLURL                 string    `json:"html_url"`
    FollowersURL            string    `json:"followers_url"`
    FollowingURL            string    `json:"following_url"`
    GistsURL                string    `json:"gists_url"`
    StarredURL              string    `json:"starred_url"`
    SubscriptionsURL        string    `json:"subscriptions_url"`
    OrganizationsURL        string    `json:"organizations_url"`
    ReposURL                string    `json:"repos_url"`
    EventsURL               string    `json:"events_url"`
    ReceivedEventsURL       string    `json:"received_events_url"`
    Type                    string    `json:"type"`
    SiteAdmin               bool      `json:"site_admin"`
    Name                    string    `json:"name"`
    Company                 string    `json:"company"`
    Blog                    string    `json:"blog"`
    Location                string    `json:"location"`
    Email                   string    `json:"email"`
    Hireable                bool      `json:"hireable"`
    Bio                     string    `json:"bio"`
    TwitterUsername         string    `json:"twitter_username"`
    PublicRepos             int       `json:"public_repos"`
    PublicGists             int       `json:"public_gists"`
    Followers               int       `json:"followers"`
    Following               int       `json:"following"`
    CreatedAt               time.Time `json:"created_at"`
    UpdatedAt               time.Time `json:"updated_at"`
    PrivateGists            int       `json:"private_gists"`
    TotalPrivateRepos       int       `json:"total_private_repos"`
    OwnedPrivateRepos       int       `json:"owned_private_repos"`
    DiskUsage               int       `json:"disk_usage"`
    Collaborators           int       `json:"collaborators"`
    TwoFactorAuthentication bool      `json:"two_factor_authentication"`
    Plan                    struct {
        Name          string `json:"name"`
        Space         int    `json:"space"`
        PrivateRepos  int    `json:"private_repos"`
        Collaborators int    `json:"collaborators"`
    } `json:"plan"`
}

type Deployment struct {
    URL                   string             `json:"url"`
    ID                    int                `json:"id"`
    NodeID                string             `json:"node_id"`
    Sha                   string             `json:"sha"`
    Ref                   string             `json:"ref"`
    Task                  string             `json:"task"`
    Payload               interface{}        `json:"payload"`
    Description           string             `json:"description"`
    Creator               *DeploymentCreator `json:"creator"`
    CreatedAt             time.Time          `json:"created_at"`
    UpdatedAt             time.Time          `json:"updated_at"`
    StatusesURL           string             `json:"statuses_url"`
    RepositoryURL         string             `json:"repository_url"`
    Environment           string             `json:"environment"`
    TransientEnvironment  bool               `json:"transient_environment"`
    OriginalEnvironment   string             `json:"original_environment"`
    ProductionEnvironment bool               `json:"production_environment"`
}

type DeploymentCreator struct {
    Login     string  `json:"login"`
    ID        int     `json:"id"`
    NodeID    string  `json:"node_id"`
    Name      *string `json:"name"`
    Email     *string `json:"email"`
    AvatarURL string  `json:"avatar_url"`
    URL       string  `json:"url"`
    HTMLURL   string  `json:"html_url"`
    Type      string  `json:"type"`
    SiteAdmin bool    `json:"site_admin"`
}

type DeploymentInfo struct {
    Deployment Deployment         `json:"deployment"`
    Statuses   []DeploymentStatus `json:"statuses"`
}
