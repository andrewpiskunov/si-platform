package game

import "slices"

func scoreOrder(s State, ids []string) []string {
	out := slices.Clone(ids)
	slices.SortStableFunc(out, func(a, b string) int {
		pa, _ := s.Participant(a)
		pb, _ := s.Participant(b)
		if pa.Score < pb.Score {
			return -1
		}
		if pa.Score > pb.Score {
			return 1
		}
		return 0
	})
	return out
}
func startAuction(s *State, now int64) {
	g := s.Gameplay
	q := g.Question
	others := []string{}
	for _, p := range q.Players {
		if p.ID != g.ChooserID && p.Maximum > q.Price {
			others = append(others, p.ID)
		}
	}
	ids := append([]string{g.ChooserID}, scoreOrder(*s, others)...)
	minimum := q.Price
	if g.Round != nil && g.Round.MinimumPrice > 0 {
		minimum = g.Round.MinimumPrice
	}
	step := int64(1)
	for step*10 <= minimum {
		step *= 10
	}
	q.Auction = &AuctionState{ActiveIDs: slices.Clone(ids), Order: ids, Step: step}
	setStage(s, "auction", g.ChooserID, now, q.Rules.WagerSeconds)
	p, _ := s.Participant(g.ChooserID)
	if p.Score < q.Price || p.Score == q.Price && len(ids) == 1 {
		acceptBid(s, g.ChooserID, q.Price, false, now)
	}
}
func acceptBid(s *State, id string, amount int64, allIn bool, now int64) {
	q := s.Gameplay.Question
	a := q.Auction
	a.LeaderID = id
	a.Amount = amount
	a.AllIn = a.AllIn || allIn
	active := []string{}
	for _, other := range a.ActiveIDs {
		p, _ := s.Participant(other)
		if other == id || p.Score > amount {
			active = append(active, other)
		}
	}
	a.ActiveIDs = active
	nextAuction(s, id, now)
}
func nextAuction(s *State, previous string, now int64) {
	q := s.Gameplay.Question
	a := q.Auction
	if a.LeaderID != "" && len(a.ActiveIDs) == 1 {
		q.RecipientID = a.LeaderID
		q.Price = a.Amount
		present(s, now)
		return
	}
	start := slices.Index(a.Order, previous)
	for i := 1; i <= len(a.Order); i++ {
		id := a.Order[(start+i)%len(a.Order)]
		if slices.Contains(a.ActiveIDs, id) && id != a.LeaderID {
			setStage(s, "auction", id, now, q.Rules.WagerSeconds)
			return
		}
	}
	if a.LeaderID != "" {
		q.RecipientID = a.LeaderID
		q.Price = a.Amount
		present(s, now)
	}
}
func auctionBid(s *State, id, mode string, amount *int64, now int64) error {
	g := s.Gameplay
	q := g.Question
	if g.Stage.Kind != "auction" || g.Stage.TurnID != id {
		return Fault("not_your_turn")
	}
	a := q.Auction
	p, _ := s.Participant(id)
	if mode == "pass" {
		if a.LeaderID == "" {
			return Fault("invalid_bid")
		} else {
			a.ActiveIDs = slices.DeleteFunc(a.ActiveIDs, func(v string) bool { return v == id })
			nextAuction(s, id, now)
		}
		return nil
	}
	value := p.Score
	if mode == "bid" {
		if a.AllIn {
			return Fault("all_in_only")
		}
		value = *amount
	}
	minimum := auctionMinimum(q)
	if value > p.Score || value < minimum && !(mode == "all_in" && value > a.Amount) || value < q.Price || mode == "bid" && value != p.Score && value%a.Step != 0 {
		return Fault("invalid_bid")
	}
	acceptBid(s, id, value, mode == "all_in", now)
	return nil
}
func chooseRecipient(s *State, target string, now int64) error {
	g := s.Gameplay
	q := g.Question
	if g.Stage.Kind != "select_recipient" || submission(q, target) == nil || q.Spec.SelectionMode == "exceptCurrent" && target == g.ChooserID {
		return Fault("invalid_recipient")
	}
	q.RecipientID = target
	if q.Spec.PriceRange == nil {
		present(s, now)
	} else {
		r := secretRange(*s)
		if r.Minimum == r.Maximum {
			q.Price = r.Minimum
			present(s, now)
		} else {
			setStage(s, "select_price", target, now, q.Rules.SelectionSeconds)
		}
	}
	return nil
}
func secretRange(s State) PriceRange {
	r := *s.Gameplay.Question.Spec.PriceRange
	if r.Maximum == 0 && s.Gameplay.Round != nil {
		r.Minimum = s.Gameplay.Round.MinimumPrice
		r.Maximum = s.Gameplay.Round.MaximumPrice
		r.Step = r.Maximum - r.Minimum
	}
	return r
}
func choosePrice(s *State, value int64, now int64) error {
	if s.Gameplay.Stage.Kind != "select_price" {
		return Fault("invalid_phase")
	}
	r := secretRange(*s)
	if value < r.Minimum || value > r.Maximum || r.Step > 0 && (value-r.Minimum)%r.Step != 0 && value != r.Maximum {
		return Fault("invalid_price")
	}
	s.Gameplay.Question.Price = value
	present(s, now)
	return nil
}
func submitWager(s *State, id string, amount int64, now int64) error {
	g := s.Gameplay
	q := g.Question
	p := submission(q, id)
	if g.Stage.Kind != "collect_wagers" || p == nil || p.Wager != nil || amount < 1 || amount > p.Maximum {
		return Fault("invalid_wager")
	}
	p.Wager = &amount
	all := true
	for _, p := range q.Players {
		all = all && p.Wager != nil
	}
	if all {
		present(s, now)
	}
	return nil
}
func closeWagers(s *State, now int64) {
	q := s.Gameplay.Question
	q.Players = slices.DeleteFunc(q.Players, func(p Submission) bool { return p.Wager == nil })
	if len(q.Players) == 0 {
		resolveQuestion(s, now, false)
	} else {
		present(s, now)
	}
}
func reveal(s *State, target string, now int64) error {
	g := s.Gameplay
	q := g.Question
	if q == nil || !q.Shown {
		return Fault("invalid_phase")
	}
	if q.Spec.Type == "stakeAll" {
		if g.Stage.Kind != "reveal" || target != g.Stage.TurnID {
			return Fault("not_your_turn")
		}
		p := submission(q, target)
		if p == nil || p.Revealed {
			return Fault("invalid_target")
		}
		p.Revealed = true
		if !startAnswerPresentation(s, now, "judge", target) {
			setStage(s, "judge", target, now, q.Rules.SelectionSeconds)
		}
		return nil
	}
	if target != "" {
		return Fault("invalid_target")
	}
	if g.Stage.Kind != "buzz_open" && g.Stage.Kind != "resolved" && g.Stage.Kind != "judge" {
		return Fault("invalid_phase")
	}
	if ordinary(q) {
		resolveQuestion(s, now, true)
	} else {
		startAnswerPresentation(s, now, g.Stage.Kind, g.Stage.TurnID)
	}
	return nil
}

// SI GameController.AskStake aligns (previous stake + step), not just previous.
func auctionMinimum(q *ActiveQuestion) int64 {
	minimum := q.Price
	a := q.Auction
	if a.LeaderID != "" {
		minimum = a.Amount + a.Step
	}
	return ((minimum + a.Step - 1) / a.Step) * a.Step
}
