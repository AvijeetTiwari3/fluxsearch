# Corpus seed script for FluxSearch Engine
param(
    [string]$BaseUrl = "http://localhost:8080"
)

$docs = @(
    @{
        title = "Designing Data-Intensive Applications"
        content = "Reliability, scalability, and maintainability in distributed databases, consensus algorithms, partition tolerance, and replication strategies."
        url = "https://dataintensive.net"
        tags = @("distributed-systems", "databases", "storage")
    },
    @{
        title = "The Google File System (GFS)"
        content = "A scalable distributed file system for large distributed data-intensive applications. Fault-tolerance, chunkservers, and master metadata."
        url = "https://research.google/pubs/pub51/"
        tags = @("google", "storage", "distributed-systems")
    },
    @{
        title = "MapReduce: Simplified Data Processing on Large Clusters"
        content = "Programming model and associated implementation for processing and generating large data sets with parallel distributed execution."
        url = "https://research.google/pubs/pub62/"
        tags = @("google", "mapreduce", "parallel-computing")
    },
    @{
        title = "In Search of an Understandable Consensus Algorithm (Raft)"
        content = "Raft is a consensus algorithm designed as an alternative to Paxos. It provides leader election, log replication, and safety guarantees."
        url = "https://raft.github.io"
        tags = @("consensus", "raft", "fault-tolerance")
    },
    @{
        title = "Spanner: Google's Globally-Distributed Database"
        content = "Spanner is Google's scalable, multi-version, globally-distributed, and synchronously-replicated database using TrueTime API."
        url = "https://research.google/pubs/pub39966/"
        tags = @("google", "spanner", "distributed-systems", "truetime")
    },
    @{
        title = "Okapi BM25 Ranking Function"
        content = "Probabilistic relevance framework based on term frequency saturation k1 and document length normalization parameter b for IR."
        url = "https://en.wikipedia.org/wiki/Okapi_BM25"
        tags = @("search", "information-retrieval", "bm25")
    },
    @{
        title = "Efficient and Robust Approximate Nearest Neighbor Search Using HNSW Graphs"
        content = "Hierarchical Navigable Small World graphs provide logarithmic search scaling for high-dimensional vector spaces and dense embeddings."
        url = "https://arxiv.org/abs/1603.09320"
        tags = @("vector-search", "hnsw", "embeddings", "ai")
    }
)

Write-Host "Seeding $($docs.Count) documents into FluxSearch at $BaseUrl..." -ForegroundColor Cyan

foreach ($doc in $docs) {
    $body = $doc | ConvertTo-Json
    try {
        $res = Invoke-RestMethod -Uri "$BaseUrl/api/v1/index" -Method Post -Body $body -ContentType "application/json"
        Write-Host " Indexed: $($doc.title) [DocID: $($res.doc_id)]" -ForegroundColor Green
    } catch {
        Write-Host " Failed to index: $($doc.title) - $($_.Exception.Message)" -ForegroundColor Red
    }
}

Write-Host "Seeding complete!" -ForegroundColor Cyan
