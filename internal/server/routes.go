package server

import (
    "encoding/json"
    "errors"
    "fmt"
    "github.com/gin-contrib/sessions"
    "github.com/gin-contrib/sessions/cookie"
    "github.com/gin-gonic/gin"
    "net/http"
    "net/url"
    "os"
    "time"
)

var (
    clientID     = os.Getenv("CLIENT_ID")
    clientSecret = os.Getenv("CLIENT_SECRET")

    // Create a new cookie store with a random key
    store = cookie.NewStore([]byte("secret"))
)

const GITHUB_API_ENDPOINT = "https://api.github.com"

func (s *Server) RegisterRoutes() http.Handler {
    r := gin.Default()
    r.LoadHTMLGlob("templates/*")
    r.Use(sessions.Sessions("cookie_session", store))

    r.GET("/", s.HomeHandler)
    r.GET("/deployments", s.DeploymentsHandler)
    r.GET("/auth/callback", s.CallbackHandler)

    return r
}

func (s *Server) HomeHandler(c *gin.Context) {
    resp := make(map[string]string)
    resp["message"] = "Hello World"

    c.HTML(http.StatusOK, "index.tmpl", gin.H{
        "clientId": clientID,
    })
}

func (s *Server) DeploymentsHandler(c *gin.Context) {
    resp := make(map[string]string)
    resp["message"] = "Hello World"

    session := sessions.Default(c)

    token := session.Get("access_token").(string)
    deployments, err := fetchUserDeployments(token)
    if err != nil {
        c.AbortWithError(http.StatusInternalServerError, errors.New("failed to fetch deployments: "+err.Error()))
        return
    }
    c.HTML(http.StatusOK, "deployments.tmpl", gin.H{
        "deployments": deployments,
    })
}

func (s *Server) CallbackHandler(c *gin.Context) {
    code := c.Query("code")
    if code == "" {
        c.AbortWithError(http.StatusBadRequest, errors.New("no code in the request"))
        return
    }

    fmt.Printf("Successfully authorized! Got code %s\n", code)

    // Exchange the code for an access token
    tokenData, err := exchangeCode(code)
    if err != nil || tokenData["access_token"] == nil {
        c.AbortWithError(http.StatusInternalServerError, errors.New("failed to exchange code - "+err.Error()))
        return
    }
    fmt.Printf("Successfully exchanged code for access_token! Got %s\n", tokenData["access_token"])

    // Fetch user info
    token := tokenData["access_token"].(string)
    fmt.Println("Setting access token: " + token)

    session := sessions.Default(c)
    session.Set("access_token", token)
    err = session.Save()
    if err != nil {
        fmt.Println(err)
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

func fetchUserDeployments(token string) (UserDeployments, error) {
    owner := "drdreo"
    repo := "habi"

    apiUrl := fmt.Sprintf("%s/repos/%s/%s/deployments", GITHUB_API_ENDPOINT, owner, repo)
    req, err := http.NewRequest("GET", apiUrl, nil)
    if err != nil {
        return nil, err
    }

    authHeader := fmt.Sprintf("Bearer %s", token)
    fmt.Println(authHeader)
    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("Authorization", authHeader)

    client := &http.Client{}
    res, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer res.Body.Close()

    //    var result interface{} // Using interface{} to handle any type of response
    //    fmt.Println(result)
    var deployments UserDeployments
    err = json.NewDecoder(res.Body).Decode(&deployments)
    //    err = json.NewDecoder(res.Body).Decode(&result)
    if err != nil {
        return nil, err
    }
    fmt.Println(deployments)

    return deployments, nil
}

type UserDeployments []struct {
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
