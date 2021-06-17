NUMBER="${1:-01}"

while true
do
    json=$(curl -s "https://echo-$NUMBER.deploy.footway.com/")
    num=$(echo "$json" | jq -r '.env.__NUMBER__')

    if [[ "$num" == "$NUMBER" ]]
    then
        echo
        date
        echo "==> DONE"
        break
    else
        echo -n .
        sleep 1
    fi
done
