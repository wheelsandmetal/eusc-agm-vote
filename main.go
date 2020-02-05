package main

import (
	"cloud.google.com/go/datastore"
	"fmt"
	"google.golang.org/api/iterator"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

const PROJECT_ID string = "eusc-agm-vote"
const AUTH_FILE string = "auth-file.json"

func main() {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", AUTH_FILE)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/election/", electionHandler)
	http.HandleFunc("/vote/", voteHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	ctx := r.Context()
	client, err := datastore.NewClient(ctx, PROJECT_ID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	q := datastore.NewQuery("Election").Order("Order")

	var elections []Election
	var election Election
	it := client.Run(ctx, q)
	_, err = it.Next(&election)
	for err == nil {
		elections = append(elections, election)
		_, err = it.Next(&election)
	}
	if err != iterator.Done {
		message := fmt.Sprintf("Failed fetching results ", err)
		errorHandler(w, r, http.StatusNotFound, message, err)
	}

	var params = templateParams{Elections: elections}
	t, _ := template.ParseFiles("tmpl/index.html")
	t.Execute(w, params)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
}

func electionHandler(w http.ResponseWriter, r *http.Request) {
	pos_key := strings.TrimPrefix(r.URL.Path, "/election/")
	pos_key = strings.Split(pos_key, "/")[0]
	if pos_key == "" {
		message := "No election specified"
		var err error
		errorHandler(w, r, http.StatusNotFound, message, err)
		return
	}

	ctx := r.Context()
	client, err := datastore.NewClient(ctx, PROJECT_ID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	q := datastore.NewQuery("Election").
		Filter("Key =", pos_key)

	var election Election
	it := client.Run(ctx, q)
	_, err = it.Next(&election)
	if err != nil || len(election.Candidates_Keys) < 1 {
		message := fmt.Sprintf("Failed to get any candidates")
		log.Fatalf("Failed fetching results: %v", err)
		errorHandler(w, r, http.StatusNotFound, message, err)
		return
	}

	if len(election.Candidates_Keys) >= 1 {
		var candidates = make([]Candidate, len(election.Candidates_Keys))

		err = client.GetMulti(ctx, election.Candidates_Keys, candidates)

		if err != nil {
			message := "No Candidates Found."
			errorHandler(w, r, http.StatusNotFound, message, err)
			return
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Key < candidates[j].Key })

		election.Candidates = candidates
		t, _ := template.ParseFiles("tmpl/vote.html")
		t.Execute(w, election)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}

}

func voteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	ctx := r.Context()
	client, err := datastore.NewClient(ctx, PROJECT_ID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	err = r.ParseForm()
	if err != nil {
		panic(err)
	}

	voter_id := r.Form.Get("voter_id")

	// Check voter_id exist in datastore
	q := datastore.NewQuery("Voter").
		Filter("Voter_ID =", voter_id).Limit(1)
	var voter Voter
	it := client.Run(ctx, q)
	_, err = it.Next(&voter)
	if err != nil {
		message := fmt.Sprintf("No voter found with this ID. Are you sure you entered your identity correctly?")
		errorHandler(w, r, http.StatusBadRequest, message, err)
		return
	}

	// Check if election is open
	election_key := r.Form.Get("election_key")
	q = datastore.NewQuery("Election").
		Filter("Key =", election_key).
		Limit(1)
	var election Election
	it = client.Run(ctx, q)
	_, err = it.Next(&election)
	if err != nil || election.Active == false {
		message := fmt.Sprintf("This current Election is NOT open right now.")
		errorHandler(w, r, http.StatusForbidden, message, err)
		return
	}

	// Check if voter voted for someone
	var candidate_key = r.Form.Get("candidate_key")
	if candidate_key == "" {
		message := fmt.Sprintf("You have to vote for someone!")
		errorHandler(w, r, http.StatusBadRequest, message, nil)
		return
	}

	// Check if voter has voted before
	q = datastore.NewQuery("Vote").
		Filter("Voter_ID =", voter.Voter_ID).
		Filter("Election_Key =", election_key).
		Limit(1)
	var vote Vote
	it = client.Run(ctx, q)
	key, err := it.Next(&vote)
	if err == nil {
		// Overwrite vote
		vote.Candidate_Key = candidate_key
		if _, err := client.Put(ctx, key, &vote); err != nil {
			message := fmt.Sprintf("Unable to put vote in DataStore")
			log.Fatalf("datastore.Put: %v", err)
			errorHandler(w, r, http.StatusInternalServerError, message, err)
			return
		}
	} else {
		// Create new vote
		vote = Vote{
			Voter_ID:      voter.Voter_ID,
			Election_Key:  election_key,
			Candidate_Key: candidate_key,
		}
		key = datastore.IncompleteKey("Vote", nil)
		if _, err := client.Put(ctx, key, &vote); err != nil {
			message := fmt.Sprintf("Unable to put vote in DataStore")
			log.Fatalf("datastore.Put: %v", err)
			errorHandler(w, r, http.StatusInternalServerError, message, err)
			return
		}
	}
	http.ServeFile(w, r, "static/thankyou.html")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
}

type Vote struct {
	Voter_ID      string
	Election_Key  string
	Candidate_Key string
}

type Voter struct {
	Voter_ID  string
	Vote_Keys []*datastore.Key
}

type Candidate struct {
	Key     string
	Name    string
	Message string
}

type Election struct {
	Key             string
	Position        string
	Candidates_Keys []*datastore.Key
	Candidates      []Candidate
	Active          bool
	Order           int
}

type templateParams struct {
	Elections []Election
}

type ErrorParams struct {
	Title       string
	TypeMessage string
	Message     string
	Error       error
}

func errorHandler(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	w.WriteHeader(status)
	var errorParams ErrorParams

	switch status {
	case http.StatusBadRequest:
		errorParams.Title = "400 BAD REQUEST"
		errorParams.TypeMessage = "The server returned a 400 code"
	case http.StatusNotFound:
		errorParams.Title = "404 NOT FOUND"
		errorParams.TypeMessage = "The server returned a 404 code"
	case http.StatusInternalServerError:
		errorParams.Title = "500 INTERNAL SERVER ERROR"
		errorParams.TypeMessage = "The server returned a 500 code"
	case http.StatusForbidden:
		errorParams.Title = "403 FORBIDDEN"
		errorParams.TypeMessage = "The server returned a 403 code"
	}

	errorParams.Message = message
	errorParams.Error = err
	t, _ := template.ParseFiles("tmpl/errorPage.html")
	t.Execute(w, errorParams)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
}
