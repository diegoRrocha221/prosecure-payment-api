#!/bin/bash

# Clear queues script for ProSecure Payment API

# Load environment variables for Redis connection
set -a
source .env
set +a

# Extract Redis password from REDIS_URL
REDIS_PASSWORD=$(echo $REDIS_URL | sed -E 's/.*:\/\/(.*):(.*)@.*/\2/')
REDIS_HOST=$(echo $REDIS_URL | sed -E 's/.*@([^:]+).*/\1/')
REDIS_PORT=$(echo $REDIS_URL | sed -E 's/.*:([0-9]+).*/\1/')

# If no password in URL, set empty auth command
if [ -z "$REDIS_PASSWORD" ]; then
	    AUTH_CMD=""
    else
	        AUTH_CMD="AUTH $REDIS_PASSWORD"
fi

# Function to execute Redis command with authentication
redis_cmd() {
	    if [ -z "$AUTH_CMD" ]; then
		            redis-cli -h $REDIS_HOST -p $REDIS_PORT $@
			        else
					        redis-cli -h $REDIS_HOST -p $REDIS_PORT $AUTH_CMD $@
						    fi
					    }

				    # Prompt for confirmation
				    echo "WARNING: This will clear ALL payment job queues!"
				    echo "This action cannot be undone. Jobs in progress may be lost."
				    read -p "Are you sure you want to continue? (y/n): " CONFIRM

				    if [ "$CONFIRM" != "y" ]; then
					        echo "Operation cancelled."
						    exit 0
				    fi

				    # Clear all queues
				    redis_cmd DEL payment_jobs
				    redis_cmd DEL payment_jobs:processing
				    redis_cmd DEL payment_jobs:failed
				    redis_cmd DEL payment_jobs:delayed

				    echo "All payment job queues have been cleared."
