s3: mkdir -p temp/logs && go run ./cmd/dev-s3 2>&1 | tee temp/logs/dev-s3.log
server: ./scripts/dev-server.sh
k8s: mkdir -p temp/logs && go run ./cmd/dev-k8s 2>&1 | tee temp/logs/dev-k8s.log
operator: mkdir -p temp/logs && ./scripts/dev-operator.sh 2>&1 | tee temp/logs/operator.log
