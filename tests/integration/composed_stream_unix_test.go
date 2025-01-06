package integration

import (
	"context"
	"github.com/kwilteam/kwil-db/core/crypto"
	"github.com/kwilteam/kwil-db/core/crypto/auth"
	"github.com/stretchr/testify/assert"
	"github.com/trufnetwork/sdk-go/core/tnclient"
	"github.com/trufnetwork/sdk-go/core/types"
	"github.com/trufnetwork/sdk-go/core/util"
	"testing"
)

// This file contains integration tests for composed streams in the Truf Network (TN).
// It demonstrates the process of deploying, initializing, and querying a composed stream
// that aggregates data from multiple primitive streams.

// TestComposedStream demonstrates the process of deploying, initializing, and querying
// a composed stream that aggregates data from multiple primitive streams in the TN using the TN SDK.
func TestComposedStreamUnix(t *testing.T) {
	ctx := context.Background()

	// Parse the private key for authentication
	pk, err := crypto.Secp256k1PrivateKeyFromHex(TestPrivateKey)
	assertNoErrorOrFail(t, err, "Failed to parse private key")

	// Create a signer using the parsed private key
	signer := &auth.EthPersonalSigner{Key: *pk}
	tnClient, err := tnclient.NewClient(ctx, TestKwilProvider, tnclient.WithSigner(signer))
	assertNoErrorOrFail(t, err, "Failed to create client")

	signerAddress, err := util.NewEthereumAddressFromBytes(signer.Identity())
	assertNoErrorOrFail(t, err, "Failed to create signer address")

	// Generate a unique stream ID and locator for the composed stream and its child streams
	streamId := util.GenerateStreamId("test-composed-stream-unix")
	streamLocator := tnClient.OwnStreamLocator(streamId)

	childAStreamId := util.GenerateStreamId("test-composed-stream-child-a-unix")
	childBStreamId := util.GenerateStreamId("test-composed-stream-child-b-unix")

	allStreamIds := []util.StreamId{streamId, childAStreamId, childBStreamId}

	// Cleanup function to destroy the streams after test completion
	t.Cleanup(func() {
		for _, id := range allStreamIds {
			destroyResult, err := tnClient.DestroyStream(ctx, id)
			assertNoErrorOrFail(t, err, "Failed to destroy stream")
			waitTxToBeMinedWithSuccess(t, ctx, tnClient, destroyResult)
		}
	})

	// Subtest for deploying, initializing, and querying the composed stream
	t.Run("DeploymentAndReadOperations", func(t *testing.T) {
		// Step 1: Deploy the composed stream
		// This creates the composed stream contract on the TN
		deployTxHash, err := tnClient.DeployStream(ctx, streamId, types.StreamTypeComposedUnix)
		assertNoErrorOrFail(t, err, "Failed to deploy composed stream")
		waitTxToBeMinedWithSuccess(t, ctx, tnClient, deployTxHash)

		// Load the deployed composed stream
		deployedComposedStream, err := tnClient.LoadComposedStream(streamLocator)
		assertNoErrorOrFail(t, err, "Failed to load composed stream")

		// Step 2: Initialize the composed stream
		// Initialization prepares the composed stream for data operations
		txHashInit, err := deployedComposedStream.InitializeStream(ctx)
		assertNoErrorOrFail(t, err, "Failed to initialize composed stream")
		waitTxToBeMinedWithSuccess(t, ctx, tnClient, txHashInit)

		// Step 3: Deploy child streams with initial data
		// Deploy two primitive child streams with initial data
		// | date       | childA | childB |
		// |------------|--------|--------|
		// | 2020-01-01 | 1      | 3      |
		// | 2020-01-02 | 2      | 4      |

		deployTestPrimitiveStreamUnixWithData(t, ctx, tnClient, childAStreamId, []types.InsertRecordUnixInput{
			{Value: 1, DateValue: 1},
			{Value: 2, DateValue: 2},
			{Value: 3, DateValue: 3},
			{Value: 4, DateValue: 4},
			{Value: 5, DateValue: 5},
		})

		deployTestPrimitiveStreamUnixWithData(t, ctx, tnClient, childBStreamId, []types.InsertRecordUnixInput{
			{Value: 3, DateValue: 1},
			{Value: 4, DateValue: 2},
			{Value: 5, DateValue: 3},
			{Value: 6, DateValue: 4},
			{Value: 7, DateValue: 5},
		})

		// Step 4: Set taxonomies for the composed stream
		// Taxonomies define the structure of the composed stream
		mockStartDate := 3
		txHashTaxonomies, err := deployedComposedStream.SetTaxonomyUnix(ctx, types.TaxonomyUnix{
			TaxonomyItems: []types.TaxonomyItem{
				{
					ChildStream: types.StreamLocator{
						StreamId:     childAStreamId,
						DataProvider: signerAddress,
					},
					Weight: 1,
				},
				{
					ChildStream: types.StreamLocator{
						StreamId:     childBStreamId,
						DataProvider: signerAddress,
					},
					Weight: 2,
				}},
			StartDate: &mockStartDate,
		})
		assertNoErrorOrFail(t, err, "Failed to set taxonomies")
		waitTxToBeMinedWithSuccess(t, ctx, tnClient, txHashTaxonomies)

		// Describe the taxonomies of the composed stream
		taxonomies, err := deployedComposedStream.DescribeTaxonomiesUnix(ctx, types.DescribeTaxonomiesParams{
			LatestVersion: true,
		})
		assertNoErrorOrFail(t, err, "Failed to describe taxonomies")
		assert.Equal(t, 2, len(taxonomies.TaxonomyItems))
		assert.Equal(t, 3, *taxonomies.StartDate)

		// Step 5: Query the composed stream for records
		// Query records within a specific date range
		mockDateFrom := 4
		mockDateTo := 5
		records, err := deployedComposedStream.GetRecordUnix(ctx, types.GetRecordUnixInput{
			DateFrom: &mockDateFrom,
			DateTo:   &mockDateTo,
		})

		assertNoErrorOrFail(t, err, "Failed to get records")
		assert.Equal(t, 2, len(records))

		// Query the records before the set start date
		mockDateFrom2 := 1
		mockDateTo2 := 2
		recordsBefore, errBefore := deployedComposedStream.GetRecordUnix(ctx, types.GetRecordUnixInput{
			DateFrom: &mockDateFrom2,
			DateTo:   &mockDateTo2,
		})
		assertNoErrorOrFail(t, errBefore, "Failed to get records before start date")
		assert.NotNil(t, recordsBefore, "Records before start date should not be nil")

		// Function to check the record values
		var checkRecord = func(record types.StreamRecordUnix, expectedValue float64) {
			val, err := record.Value.Float64()
			assertNoErrorOrFail(t, err, "Failed to parse value")
			assert.Equal(t, expectedValue, val)
		}

		// Verify the record values
		// (( v1 * w1 ) + ( v2 * w2 )) / (w1 + w2)
		// (( 4 *  1 ) + (  6 *  2 )) / ( 1 +  2) = 16 / 3 = 5.333
		// (( 5 *  1 ) + (  7 *  2 )) / ( 1 +  2) = 19 / 3 = 6.333
		checkRecord(records[0], 5.333333333333333)
		checkRecord(records[1], 6.333333333333333)

		// Step 6: Query the composed stream for index
		// Query the index within a specific date range
		mockDateFrom3 := 3
		mockDateTo3 := 4
		mockBaseDate := 3
		index, err := deployedComposedStream.GetIndexUnix(ctx, types.GetIndexUnixInput{
			DateFrom: &mockDateFrom3,
			DateTo:   &mockDateTo3,
			BaseDate: &mockBaseDate,
		})

		assertNoErrorOrFail(t, err, "Failed to get index")
		assert.Equal(t, 2, len(index))
		checkRecord(index[0], 100)                // index on base date is expected to be 100
		checkRecord(index[1], 124.44444444444444) // it is x% away from the base date + 1 in percentage

		// Query the index before the set start date
		mockDateFrom4 := 4
		mockDateTo4 := 5
		indexBefore, errBefore := deployedComposedStream.GetIndexUnix(ctx, types.GetIndexUnixInput{
			DateFrom: &mockDateFrom4,
			DateTo:   &mockDateTo4,
		})
		assertNoErrorOrFail(t, errBefore, "Failed to get index before start date")
		assert.NotNil(t, indexBefore, "Index before start date should not be nil")
	})
}