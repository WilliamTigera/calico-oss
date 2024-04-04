package authorization

import (
	"strings"

	"github.com/projectcalico/calico/kube-controllers/pkg/elasticsearch"
	eusers "github.com/projectcalico/calico/kube-controllers/pkg/elasticsearch/users"
	log "github.com/sirupsen/logrus"
)

func (n *nativeUserSynchronizer) eeResync() error {
	users, err := n.esCLI.GetUsers()
	if err != nil {
		return err
	}

	for _, user := range users {
		// Exclude Tigera's system users from deletion.
		if user.FullName != eusers.SystemUserFullName {
			subjectID := strings.TrimPrefix(user.Username, n.esUserPrefix)
			if !n.userCache.Exists(subjectID) {
				log.WithField("subjectId", subjectID).Trace("deleting user from Elasticsearch as it is not present in our cache")
				if err := n.esCLI.DeleteUser(elasticsearch.User{Username: user.Username}); err != nil {
					return err
				}
			}
		}
	}

	subjects := n.userCache.SubjectIDs()
	return n.synchronizeOIDCUsers(subjects)
}
