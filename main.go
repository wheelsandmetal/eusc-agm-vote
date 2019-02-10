package main

import (
	"fmt"
	"strings"
	"html/template"
	"net/http"
	"google.golang.org/appengine" // Required external App Engine library
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/datastore"
)

func main() {
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/election/", electionHandler)
	http.HandleFunc("/vote/", voteHandler)
	appengine.Main() // Starts the server to receive requests
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	ctx := appengine.NewContext(r)
	q := datastore.NewQuery("Election").Order("Order")

	var elections []Election
	var election Election
	it := q.Run(ctx)
	_, err := it.Next(&election)
	for err == nil {
		elections = append(elections, election)
		_, err = it.Next(&election)
	}
	if err != datastore.Done {
		message := fmt.Sprintf("Failed fetching results", err)
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

	ctx := appengine.NewContext(r)
	q := datastore.NewQuery("Election").
		Filter("Key =", pos_key)

	var election Election
	it := q.Run(ctx)
	_, err := it.Next(&election)
	if err != nil || len(election.Candidates_Keys) < 1 {
		message := fmt.Sprintf("Failed to get any candidates")
		log.Errorf(ctx, "Failed fetching results: %v", err)
		errorHandler(w, r, http.StatusNotFound, message, err)
		return
	}

	if len(election.Candidates_Keys) >= 1 {
		var candidates []Candidate

		for _, key := range election.Candidates_Keys {
			var candidate Candidate
			q := datastore.NewQuery("Candidate").
				Filter("__key__ =", key)

			it := q.Run(ctx)
			_, err := it.Next(&candidate)
			if err != nil {
				message := "Candidate with key not found"
				errorHandler(w, r, http.StatusNotFound, message, err)
				return
			}
			candidates = append(candidates, candidate)
		}
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

	err := r.ParseForm()
	if err != nil {
		panic(err)
	}

	voter_id := r.Form.Get("voter_id")
	ctx := appengine.NewContext(r)

	// Check voter_id exist in datastore
	q := datastore.NewQuery("Voter").
		Filter("Voter_ID =", voter_id).Limit(1)
	var voter Voter
	it := q.Run(ctx)
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
	it = q.Run(ctx)
	_, err = it.Next(&election)
	if err != nil || election.Active == false {
		message := fmt.Sprintf("This current Election is NOT open right now.")
		errorHandler(w, r, http.StatusForbidden, message, err)
		return
	}

	// Check if voter has voted before
	q = datastore.NewQuery("Vote").
		Filter("Voter_ID =", voter.Voter_ID).
		Filter("Election_Key =", election_key).
		Limit(1)
	var vote Vote
	it = q.Run(ctx)
	key, err := it.Next(&vote)
	if err == nil {
		// Overwrite vote
		vote.Candidate_Key = r.Form.Get("candidate_key")
		if _, err := datastore.Put(ctx, key, &vote); err != nil {
			message := fmt.Sprintf("Unable to put vote in DataStore")
			log.Errorf(ctx, "datastore.Put: %v", err)
			errorHandler(w, r, http.StatusInternalServerError, message, err)
			return
		}
	} else {
		// Create new vote
		vote = Vote{
			Voter_ID: voter.Voter_ID,
			Election_Key: election_key,
			Candidate_Key: r.Form.Get("candidate_key"),
		}
		key = datastore.NewIncompleteKey(ctx, "Vote", nil)
		if _, err := datastore.Put(ctx, key, &vote); err != nil {
			message := fmt.Sprintf("Unable to put vote in DataStore")
			log.Errorf(ctx, "datastore.Put: %v", err)
			errorHandler(w, r, http.StatusInternalServerError, message, err)
			return
		}
	}
	fmt.Fprintf(w, "YOU HAVE VOTED")
}

type Vote struct {
	Voter_ID string
	Election_Key string
	Candidate_Key string
}

type Voter struct {
	Voter_ID string
	Vote_Keys []*datastore.Key
}

type Candidate struct {
	Key string
	Name string
	Message string
}

type Election struct {
	Key string
	Position string
	Candidates_Keys []*datastore.Key
	Candidates []Candidate
	Active bool
	Order int
}

type templateParams struct {
	Elections []Election
}

type ErrorParams struct {
	Title string
	TypeMessage string
	Message string
	Error error
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
