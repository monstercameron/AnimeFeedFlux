package history

import affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"

// RunsClient and ItemsClient are exactly the generated affv1 service
// client interfaces (gen/aff/v1 is generated code, not one of the five
// directories other agents own concurrently — see doc.go). Named here so
// callers wiring web/wsconn's *Conn into this page have one obvious
// place to see what it needs: hand over Conn's RunServiceClient and
// ItemServiceClient once wsconn grows those fields.
type RunsClient = affv1.RunServiceClient
type ItemsClient = affv1.ItemServiceClient
