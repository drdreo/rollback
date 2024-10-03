package server

import (
    "bytes"
    "encoding/json"
    "errors"
    "github.com/gin-gonic/gin"
    "net/http"
    "os"
)

var (
    clientID     = os.Getenv("CLIENT_ID")
    clientSecret = os.Getenv("CLIENT_SECRET")
)

func (s *Server) RegisterRoutes() http.Handler {
    r := gin.Default()
    r.LoadHTMLGlob("templates/*")

    r.GET("/", s.HomeHandler)

    return r
}

func (s *Server) HomeHandler(c *gin.Context) {
    resp := make(map[string]string)
    resp["message"] = "Hello World"

    c.HTML(http.StatusOK, "index.tmpl", gin.H{
        "clientId": clientID,
    })
}

func (s *Server) CallbackHandler(c *gin.Context) {
    code := c.Query("code")
    if code == "" {
        c.AbortWithError(http.StatusBadRequest, errors.New("no code in the request"))
        return
    }

    // Exchange the code for an access token
    tokenData, err := exchangeCode(code)
    if err != nil || tokenData["access_token"] == nil {
        c.AbortWithError(http.StatusInternalServerError, errors.New("failed to exchange code"))
        return
    }

    c.JSON(http.StatusAccepted, gin.H{"jawoi": 123})
    //    // Fetch user info
    //    token := tokenData["access_token"].(string)
    //    userInfo, err := fetchUserInfo(token)
    //    if err != nil {
    //        http.Error(w, "Failed to fetch user info", http.StatusInternalServerError)
    //        return
    //    }
    //
    //    // Render user info
    //    handle := userInfo["login"].(string)
    //    name := userInfo["name"].(string)
    //    render := fmt.Sprintf("Successfully authorized! Welcome, %s (%s).", name, handle)
    //    fmt.Fprintf(w, render)

}

// Exchange the authorization code for an access token
func exchangeCode(code string) (map[string]interface{}, error) {

    data := map[string]string{
        "client_id":     clientID,
        "client_secret": clientSecret,
        "code":          code,
    }
    body, err := json.Marshal(data)
    if err != nil {
        return nil, err
    }

    resp, err := http.Post(
        "https://github.com/login/oauth/access_token",
        "application/json",
        bytes.NewBuffer(body),
    )
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
