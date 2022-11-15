package node

import (
	"context"
	"fmt"

	subChain "github.com/FavorLabs/favorX/pkg/chain"
	chainTraffic "github.com/FavorLabs/favorX/pkg/chain/rpc/traffic"
	"github.com/FavorLabs/favorX/pkg/crypto"
	"github.com/FavorLabs/favorX/pkg/logging"
	"github.com/FavorLabs/favorX/pkg/p2p/libp2p"
	"github.com/FavorLabs/favorX/pkg/settlement"
	"github.com/FavorLabs/favorX/pkg/settlement/chain/oracle"
	"github.com/FavorLabs/favorX/pkg/settlement/pseudosettle"
	"github.com/FavorLabs/favorX/pkg/settlement/traffic"
	chequePkg "github.com/FavorLabs/favorX/pkg/settlement/traffic/cheque"
	"github.com/FavorLabs/favorX/pkg/settlement/traffic/trafficprotocol"
	"github.com/FavorLabs/favorX/pkg/storage"
	"github.com/FavorLabs/favorX/pkg/subscribe"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
)

// InitChain will initialize the Ethereum backend at the given endpoint and
// set up the Transaction Service to interact with it using the provided signer.
func InitChain(
	ctx context.Context,
	logger logging.Logger,
	subClient *subChain.Client,
	endpoint string,
	oracleContractAddress string,
	stateStore storage.StateStorer,
	localStore storage.Storer,
	signer crypto.Signer,
	trafficEnable bool,
	trafficContractAddr string,
	p2pService *libp2p.Service,
	subPub subscribe.SubPub,
) (oracle.Resolver, settlement.Interface, traffic.ApiInterface, error) {

	address := signer.Public().Encode()
	accountId, _ := types.NewAccountID(address)
	oracleServer, err := oracle.NewServer(logger, subClient, subPub)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new oracle service: %w", err)
	}
	if !trafficEnable {
		service := pseudosettle.New(p2pService, logger, stateStore, *accountId)
		if err = service.Init(); err != nil {
			return nil, nil, nil, fmt.Errorf("InitTraffic:: %w", err)
		}
		return oracleServer, service, service, nil
	}

	trafficService, err := InitTraffic(stateStore, localStore, *accountId, subClient.Traffic, logger, p2pService, signer, subPub)
	if err != nil {
		return nil, nil, nil, err
	}
	err = trafficService.Init()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("InitChain: %w", err)
	}

	return oracleServer, trafficService, trafficService, nil
}

func InitTraffic(store storage.StateStorer, localStore storage.Storer, address types.AccountID,
	transactionService chainTraffic.Interface, logger logging.Logger, p2pService *libp2p.Service, signer crypto.Signer, subPub subscribe.SubPub) (*traffic.Service, error) {
	chequeStore := chequePkg.NewChequeStore(store, address, chequePkg.RecoverCheque)
	cashOut := chequePkg.NewCashoutService(store, transactionService, chequeStore)
	addressBook := traffic.NewAddressBook(store)
	protocol := trafficprotocol.New(p2pService, logger, address)
	if err := p2pService.AddProtocol(protocol.Protocol()); err != nil {
		return nil, fmt.Errorf("traffic server :%v", err)
	}
	chequeSigner := chequePkg.NewChequeSigner(signer)
	trafficService := traffic.New(logger, address, store, localStore, transactionService, chequeStore, cashOut, p2pService, addressBook, chequeSigner, protocol, subPub)
	protocol.SetTraffic(trafficService)
	return trafficService, nil
}
