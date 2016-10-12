/*
 * Minio Cloud Storage, (C) 2016 Minio, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"errors"
	"sync"
	"time"
)

// errServerNotInitialized - server not initialized.
var errServerNotInitialized = errors.New("Server not initialized, please try again.")

// errServerVersionMismatch - server versions do not match.
var errServerVersionMismatch = errors.New("Server versions do not match.")

// errServerTimeMismatch - server times are too far apart.
var errServerTimeMismatch = errors.New("Server times are too far apart.")

/// Auth operations

// Login - login handler.
func (c *controlAPIHandlers) LoginHandler(args *RPCLoginArgs, reply *RPCLoginReply) error {
	jwt, err := newJWT(defaultInterNodeJWTExpiry)
	if err != nil {
		return err
	}
	if err = jwt.Authenticate(args.Username, args.Password); err != nil {
		return err
	}
	token, err := jwt.GenerateToken(args.Username)
	if err != nil {
		return err
	}
	reply.Token = token
	reply.Timestamp = time.Now().UTC()
	reply.ServerVersion = Version
	return nil
}

// HealListArgs - argument for ListObjects RPC.
type HealListArgs struct {
	// Authentication token generated by Login.
	GenericArgs

	Bucket    string
	Prefix    string
	Marker    string
	Delimiter string
	MaxKeys   int
}

// HealListReply - reply object by ListObjects RPC.
type HealListReply struct {
	IsTruncated bool
	NextMarker  string
	Objects     []string
}

// ListObjects - list all objects that needs healing.
func (c *controlAPIHandlers) ListObjectsHealHandler(args *HealListArgs, reply *HealListReply) error {
	objAPI := c.ObjectAPI()
	if objAPI == nil {
		return errServerNotInitialized
	}
	if !isRPCTokenValid(args.Token) {
		return errInvalidToken
	}
	info, err := objAPI.ListObjectsHeal(args.Bucket, args.Prefix, args.Marker, args.Delimiter, args.MaxKeys)
	if err != nil {
		return err
	}
	reply.IsTruncated = info.IsTruncated
	reply.NextMarker = info.NextMarker
	for _, obj := range info.Objects {
		reply.Objects = append(reply.Objects, obj.Name)
	}
	return nil
}

// HealObjectArgs - argument for HealObject RPC.
type HealObjectArgs struct {
	// Authentication token generated by Login.
	GenericArgs

	// Name of the bucket.
	Bucket string

	// Name of the object.
	Object string
}

// HealObjectReply - reply by HealObject RPC.
type HealObjectReply struct{}

// HealObject - heal the object.
func (c *controlAPIHandlers) HealObjectHandler(args *HealObjectArgs, reply *GenericReply) error {
	objAPI := c.ObjectAPI()
	if objAPI == nil {
		return errServerNotInitialized
	}
	if !isRPCTokenValid(args.Token) {
		return errInvalidToken
	}
	return objAPI.HealObject(args.Bucket, args.Object)
}

// HealObject - heal the object.
func (c *controlAPIHandlers) HealDiskMetadataHandler(args *GenericArgs, reply *GenericReply) error {
	if !isRPCTokenValid(args.Token) {
		return errInvalidToken
	}
	err := repairDiskMetadata(c.StorageDisks)
	if err != nil {
		return err
	}
	go func() {
		globalWakeupCh <- struct{}{}
	}()
	return err
}

// ServiceArgs - argument for Service RPC.
type ServiceArgs struct {
	// Authentication token generated by Login.
	GenericArgs

	// Represents the type of operation server is requested
	// to perform. Currently supported signals are
	// stop, restart and status.
	Signal serviceSignal
}

// ServiceReply - represents service operation success info.
type ServiceReply struct {
	StorageInfo StorageInfo
}

// Remote procedure call, calls serviceMethod with given input args.
func (c *controlAPIHandlers) remoteServiceCall(args *ServiceArgs, replies []*ServiceReply) error {
	var wg sync.WaitGroup
	var errs = make([]error, len(c.RemoteControls))
	// Send remote call to all neighboring peers to restart minio servers.
	for index, clnt := range c.RemoteControls {
		wg.Add(1)
		go func(index int, client *AuthRPCClient) {
			defer wg.Done()
			errs[index] = client.Call("Control.ServiceHandler", args, replies[index])
			errorIf(errs[index], "Unable to initiate control service request to remote node %s", client.Node())
		}(index, clnt)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Service - handler for sending service signals across many servers.
func (c *controlAPIHandlers) ServiceHandler(args *ServiceArgs, reply *ServiceReply) error {
	if !isRPCTokenValid(args.Token) {
		return errInvalidToken
	}
	objAPI := c.ObjectAPI()
	if objAPI == nil {
		return errServerNotInitialized
	}
	if args.Signal == serviceStatus {
		reply.StorageInfo = objAPI.StorageInfo()
		return nil
	}
	var replies = make([]*ServiceReply, len(c.RemoteControls))
	switch args.Signal {
	case serviceRestart:
		if args.Remote {
			// Set remote as false for remote calls.
			args.Remote = false
			if err := c.remoteServiceCall(args, replies); err != nil {
				return err
			}
		}
		globalServiceSignalCh <- serviceRestart
	case serviceStop:
		if args.Remote {
			// Set remote as false for remote calls.
			args.Remote = false
			if err := c.remoteServiceCall(args, replies); err != nil {
				return err
			}
		}
		globalServiceSignalCh <- serviceStop
	}
	return nil
}

// LockInfo - RPC control handler for `minio control lock`. Returns the info of the locks held in the system.
func (c *controlAPIHandlers) TryInitHandler(args *GenericArgs, reply *GenericReply) error {
	if !isRPCTokenValid(args.Token) {
		return errInvalidToken
	}
	go func() {
		globalWakeupCh <- struct{}{}
	}()
	*reply = GenericReply{}
	return nil
}