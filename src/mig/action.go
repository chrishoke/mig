/* Mozilla InvestiGator

Version: MPL 1.1/GPL 2.0/LGPL 2.1

The contents of this file are subject to the Mozilla Public License Version
1.1 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at
http://www.mozilla.org/MPL/

Software distributed under the License is distributed on an "AS IS" basis,
WITHOUT WARRANTY OF ANY KIND, either express or implied. See the License
for the specific language governing rights and limitations under the
License.

The Initial Developer of the Original Code is
Mozilla Corporation
Portions created by the Initial Developer are Copyright (C) 2014
the Initial Developer. All Rights Reserved.

Contributor(s):
Julien Vehent jvehent@mozilla.com [:ulfr]

Alternatively, the contents of this file may be used under the terms of
either the GNU General Public License Version 2 or later (the "GPL"), or
the GNU Lesser General Public License Version 2.1 or later (the "LGPL"),
in which case the provisions of the GPL or the LGPL are applicable instead
of those above. If you wish to allow use of your version of this file only
under the terms of either the GPL or the LGPL, and not to allow others to
use your version of this file under the terms of the MPL, indicate your
decision by deleting the provisions above and replace them with the notice
and other provisions required by the GPL or the LGPL. If you do not delete
the provisions above, a recipient may use your version of this file under
the terms of any one of the MPL, the GPL or the LGPL.
*/

package mig

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"math/rand"
	"mig/pgp"
	"mig/pgp/verify"
	"strconv"
	"time"
)

// an Action is the json object that is created by an investigator
// and provided to the MIG platform. It must be PGP signed.
type Action struct {
	ID             float64        `json:"id"`
	Name           string         `json:"name"`
	Target         string         `json:"target"`
	Description    Description    `json:"description,omitempty"`
	Threat         Threat         `json:"threat,omitempty"`
	ValidFrom      time.Time      `json:"validfrom"`
	ExpireAfter    time.Time      `json:"expireafter"`
	Operations     []Operation    `json:"operations"`
	PGPSignatures  []string       `json:"pgpsignatures"`
	Investigators  []Investigator `json:"investigators,omitempty"`
	Status         string         `json:"status,omitempty"`
	StartTime      time.Time      `json:"starttime,omitempty"`
	FinishTime     time.Time      `json:"finishtime,omitempty"`
	LastUpdateTime time.Time      `json:"lastupdatetime,omitempty"`
	Counters       counters       `json:"counters,omitempty"`
	SyntaxVersion  uint16         `json:"syntaxversion,omitempty"`
}

// Some counters used to track the completion of an action
type counters struct {
	Sent      int `json:"sent,omitempty"`
	Returned  int `json:"returned,omitempty"`
	Done      int `json:"done,omitempty"`
	Cancelled int `json:"cancelled,omitempty"`
	Failed    int `json:"failed,omitempty"`
	TimeOut   int `json:"timeout,omitempty"`
}

// a description is a simple object that contains detail about the
// action's author, and it's revision.
type Description struct {
	Author   string  `json:"author,omitempty"`
	Email    string  `json:"email,omitempty"`
	URL      string  `json:"url,omitempty"`
	Revision float64 `json:"revision,omitempty"`
}

// a threat provides the investigator with an idea of how dangerous
// a the compromission might be, if the indicators return positive
type Threat struct {
	Ref    string `json:"ref,omitempty"`
	Level  string `json:"level,omitempty"`
	Family string `json:"family,omitempty"`
	Type   string `json:"type,omitempty"`
}

// an operation is an object that map to an agent module.
// the parameters of the operation are passed to the module as argument,
// and thus their format depend on the module itself.
type Operation struct {
	Module     string      `json:"module"`
	Parameters interface{} `json:"parameters"`
}

// ActionFromFile() reads an action from a local file on the file system
// and returns a mig.Action structure
func ActionFromFile(path string) (Action, error) {
	var err error
	var a Action
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("mig.ActionFromFile(): %v", e)
		}
	}()
	// parse the json of the action into a mig.Action
	fd, err := ioutil.ReadFile(path)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(fd, &a)
	if err != nil {
		return a, err
	}

	return a, err
}

// GenID returns an ID composed of a unix timestamp and a random CRC32
func GenID() float64 {
	h := crc32.NewIEEE()
	t := time.Now().UTC().Format(time.RFC3339Nano)
	r := rand.New(rand.NewSource(65537))
	rand := string(r.Intn(1000000000))
	h.Write([]byte(t + rand))
	// concatenate timestamp and hash into 64 bits ID
	// id = <32 bits unix ts><32 bits CRC hash>
	uid := uint64(time.Now().Unix())
	uid = uid << 32
	sum := float64(h.Sum32())
	id := float64(uid) + sum
	return id
}

// GenHexID returns a string with an hexadecimal encoded ID
func GenB32ID() string {
	id := GenID()
	return strconv.FormatFloat(id, 'f', 6, 64)
}

// Validate verifies that the Action received contained all the
// necessary fields, and returns an error when it doesn't.
func (a Action) Validate() (err error) {
	if a.Name == "" {
		return errors.New("Action.Name is empty. Expecting string.")
	}
	if a.Target == "" {
		return errors.New("Action.Target is empty. Expecting string.")
	}
	if a.SyntaxVersion < 1 {
		return errors.New("SyntaxVersion is empty. Expecting string.")
	}
	if a.ValidFrom.String() == "" {
		return errors.New("Action.ValidFrom is empty. Expecting string.")
	}
	if a.ExpireAfter.String() == "" {
		return errors.New("Action.ExpireAfter is empty. Expecting string.")
	}
	if a.ValidFrom.After(a.ExpireAfter) {
		return errors.New("Action.ExpireAfter is set before Action.ValidFrom.")
	}
	if time.Now().After(a.ExpireAfter) {
		return errors.New("Action.ExpireAfter is passed. Action has expired.")
	}
	if a.Operations == nil {
		return errors.New("Action.Operations is nil. Expecting string.")
	}
	if len(a.PGPSignatures) < 1 {
		return errors.New("Action.PGPSignatures is empty. Expecting array of strings.")
	}
	return
}

// VerifySignatures verifies that the Action contains valid signatures from
// known investigators. It does not verify permissions.
func (a Action) VerifySignatures(keyring io.Reader) (err error) {
	astr, err := a.String()
	if err != nil {
		return errors.New("Failed to stringify action")
	}
	for _, sig := range a.PGPSignatures {
		valid, _, err := verify.Verify(astr, sig, keyring)
		if err != nil {
			return errors.New("Failed to verify PGP Signature")
		}
		if !valid {
			return errors.New("Invalid PGP Signature")
		}
	}
	return
}

//  concatenates Action components into a string
func (a Action) String() (str string, err error) {
	str = "name=" + a.Name + "; "
	str += "target=" + a.Target + "; "
	str += "validfrom=" + a.ValidFrom.String() + "; "
	str += "expireafter=" + a.ExpireAfter.String() + "; "

	args, err := json.Marshal(a.Operations)
	str += "operations='" + fmt.Sprintf("%s", args) + "';"

	return
}

// VerifyACL controls that an action has been issued by investigators
// that have the right permissions. This function looks at each operation
// listed in the action, and find the corresponding permission. If no
// permission is found, the default one `default` is used.
// The first permission that is found to apply to an operation, but
// doesn't allow the operation to run, will fail the verification globally
func (a Action) VerifyACL(acl ACL, keyring io.Reader) (err error) {
	// first, verify all signatures and get a list of PGP
	// fingerprints of the signers
	var fingerprints []string
	astr, err := a.String()
	if err != nil {
		return errors.New("Failed to stringify action")
	}
	for _, sig := range a.PGPSignatures {
		fp, err := pgp.GetFingerprintFromSignature(astr, sig, keyring)
		if err != nil {
			return fmt.Errorf("Failed to retrieve fingerprint from signatures: %v", err)
		}
		fingerprints = append(fingerprints, fp)
	}

	// Then, for each operation contained in the action, look for
	// a permission that apply to it, by comparing the operation name
	// with permission name. If none is found, use the default permission.
	for _, operation := range a.Operations {
		for _, permission := range acl {
			for permName, _ := range permission {
				if permName == operation.Module {
					return verifyPermission(operation, permName, permission, fingerprints)
				}
			}
		}
		// no specific permission found, apply the default permission
		var defaultPerm Permission
		for _, permission := range acl {
			for permName, _ := range permission {
				if permName == "default" {
					defaultPerm = permission
					break
				}
			}
		}
		return verifyPermission(operation, "default", defaultPerm, fingerprints)
	}
	return
}
