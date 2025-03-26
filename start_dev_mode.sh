#!/bin/bash

# Startup script for ProSecure Payment API with Redis background processing

# Load environment variables
set -a
source .env
set +a

# Check if Redis is running
echo "Checking Redis connection..."
if ! redis-cli ping &>/dev/null; then
	    echo "Redis is not running. Please start Redis first."
	        echo "  sudo systemctl start redis"
		    exit 1
fi
echo "Redis connection successful."

# Check if API binary exists
if [ ! -f "./payment-api" ]; then
	    echo "Payment API binary not found. Building..."
	        go build -o payment-api .
fi

# Start API server
echo "Starting ProSecure Payment API..."
./payment-api &
API_PID=$!

# Trap SIGTERM and SIGINT to gracefully shutdown both processes
trap 'echo "Shutting down..."; kill $API_PID; wait $API_PID; echo "Done."; exit 0' SIGTERM SIGINT

echo "ProSecure Payment API running with PID: $API_PID"
echo "Press Ctrl+C to stop all services"

# Wait for processes
wait $API_PID
